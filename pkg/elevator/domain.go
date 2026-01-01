package elevator

import (
	"fmt"
	"math"
	"sort"
)

// --- Domain Entities & Value Objects ---

// DoorSide is a bitmask representing the door location.
// DoorSide는 문의 위치를 나타내는 비트마스크입니다.
type DoorSide int

const (
	Front DoorSide       = 1 << iota // 1: 앞문
	Rear                             // 2: 뒷문
	Both  = Front | Rear             // 3: 양쪽 문
)

func (d DoorSide) String() string {
	return [...]string{"Front", "Rear", "Both"}[d]
}

// Direction indicates the vertical movement vector.
// Direction은 수직 이동 벡터를 나타냅니다.
type Direction string

const (
	DirUp   Direction = "Up"
	DirDown Direction = "Down"
	DirNone Direction = "None"
)

// DoorState represents the physical state of the door.
// DoorState는 문의 물리 상태를 나타냅니다.
type DoorState string

const (
	DoorOpen    DoorState = "Open"
	DoorOpening DoorState = "Opening"
	DoorClosing DoorState = "Closing"
	DoorClose   DoorState = "Close"
)

// FloorConfig holds specific settings for a single floor.
// FloorConfig는 단일 층의 특정 설정을 저장합니다.
type FloorConfig struct {
	FloorNumber  int      // 층 번호
	IsAccessible bool     // 접근 가능 여부
	OpenDoorSide DoorSide // 해당 층 도착시 문 열림 방향
}

// LogicConfig holds static configuration for the domain logic.
// LogicConfig는 도메인 로직을 위한 정적 설정입니다.
type LogicConfig struct {
	MinFloor     int
	MaxFloor     int
	InitialFloor int
	MaxWeight    int
	FloorConfigs map[int]FloorConfig
}

// LogicActionType defines the action decided by the logic.
// LogicActionType은 로직에 의해 결정된 동작을 정의합니다.
type LogicActionType int

const (
	ActionNone      LogicActionType = iota
	ActionMove                      // 이동 (Wait is implied if duration > 0)
	ActionOpenDoor                  // 문 열기 (Arrived)
	ActionCloseDoor                 // 문 닫기
	ActionStop                      // 정지
)

// LogicAction represents the decision made by the decided step.
// LogicAction은 결정된 단계에 대한 동작을 나타냅니다.
type LogicAction struct {
	Type   LogicActionType
	Target int       // Move 시 목표 층, 혹은 관련 층
	Dir    Direction // 이동 방향
}

// ElevatorLogic contains purely business logic for the elevator.
// ElevatorLogic은 엘리베이터의 순수 비즈니스 로직을 포함합니다.
// No mutex, No channel, No time.
type ElevatorLogic struct {
	Config LogicConfig

	// State
	Floor     int
	Direction Direction
	Doors     map[DoorSide]DoorState
	Weight    int
	Calls     map[int]bool // Set of called floors
}

// NewElevatorLogic creates a new logic instance.
func NewElevatorLogic(cfg LogicConfig) *ElevatorLogic {
	if cfg.FloorConfigs == nil {
		cfg.FloorConfigs = make(map[int]FloorConfig)
	}

	// Default Logic: Fill missing configs
	for i := cfg.MinFloor; i <= cfg.MaxFloor; i++ {
		if _, ok := cfg.FloorConfigs[i]; !ok {
			cfg.FloorConfigs[i] = FloorConfig{
				FloorNumber:  i,
				IsAccessible: true,
				OpenDoorSide: Front,
			}
		}
	}

	return &ElevatorLogic{
		Config:    cfg,
		Floor:     cfg.InitialFloor,
		Direction: DirNone,
		Doors: map[DoorSide]DoorState{
			Front: DoorClose,
			Rear:  DoorClose,
		},
		Calls: make(map[int]bool),
	}
}

// AddCall registers a call if valid.
func (l *ElevatorLogic) AddCall(floor int) error {
	if floor < l.Config.MinFloor || floor > l.Config.MaxFloor {
		return fmt.Errorf("floor %d out of range", floor)
	}
	cfg := l.Config.FloorConfigs[floor]
	if !cfg.IsAccessible {
		return fmt.Errorf("floor %d is inaccessible", floor)
	}
	l.Calls[floor] = true
	return nil
}

// RemoveCall removes a call.
func (l *ElevatorLogic) RemoveCall(floor int) {
	delete(l.Calls, floor)
}

// SetDoor updates door state directly (for internal logic transitions).
func (l *ElevatorLogic) SetDoor(side DoorSide, state DoorState) {
	l.Doors[side] = state
}

// SetFloor updates current floor.
func (l *ElevatorLogic) SetFloor(f int) {
	l.Floor = f
}

// SetDirection updates current direction.
func (l *ElevatorLogic) SetDirection(d Direction) {
	l.Direction = d
}

// DecideNextStep determines what the elevator should do next based on current state.
// This implements the core SCAN algorithm and Door logic.
func (l *ElevatorLogic) DecideNextStep() LogicAction {
	// 1. Check Doors
	// If any door is not closed, we generally cannot move, unless we are closing them.
	// But logic here just decides "What to do".
	// If doors are OPEN, we might need to CLOSE them (if timer expired - logic doesn't know timer, pass external trigger?
	// Or logic just checks state).
	// Ideally, Logic doesn't handle 'Time'. Service tells logic "DoorTimerExpired".

	// For now, let's implement the movement decision (SCAN).
	// Assuming doors are Closed.

	if !l.AreDoorsClosed() {
		return LogicAction{Type: ActionNone} // Cannot move if doors open
	}

	if len(l.Calls) == 0 {
		if l.Direction != DirNone {
			return LogicAction{Type: ActionStop, Dir: DirNone}
		}
		return LogicAction{Type: ActionNone}
	}

	target, found := l.selectNextTarget()
	if !found {
		// Should stop
		if l.Direction != DirNone {
			return LogicAction{Type: ActionStop, Dir: DirNone}
		}
		return LogicAction{Type: ActionNone}
	}

	// Logic specifics:
	if target == l.Floor {
		return LogicAction{Type: ActionOpenDoor, Target: target}
	}

	var dir Direction
	if target > l.Floor {
		dir = DirUp
	} else {
		dir = DirDown
	}

	return LogicAction{Type: ActionMove, Target: target, Dir: dir}
}

// AreDoorsClosed checks if all doors are closed.
func (l *ElevatorLogic) AreDoorsClosed() bool {
	for _, state := range l.Doors {
		if state != DoorClose {
			return false
		}
	}
	return true
}

// selectNextTarget implements SCAN algorithm.
func (l *ElevatorLogic) selectNextTarget() (int, bool) {
	if len(l.Calls) == 0 {
		return 0, false
	}

	// Phase 1: Current Direction Scan
	switch l.Direction {
	case DirUp:
		minDist := math.MaxInt64
		target := -1
		found := false
		for f := range l.Calls {
			if f >= l.Floor { // Include current floor
				dist := f - l.Floor
				if dist < minDist {
					minDist = dist
					target = f
					found = true
				}
			}
		}
		if found {
			return target, true
		}
	case DirDown:
		minDist := math.MaxInt64
		target := -1
		found := false
		for f := range l.Calls {
			if f <= l.Floor { // Include current floor
				dist := l.Floor - f
				if dist < minDist {
					minDist = dist
					target = f
					found = true
				}
			}
		}
		if found {
			return target, true
		}
	}

	// Phase 2: Nearest Call (Direction Reversal or Idle)
	minDist := math.MaxInt64
	target := -1
	found := false

	for f := range l.Calls {
		dist := int(math.Abs(float64(f - l.Floor)))
		if dist < minDist {
			minDist = dist
			target = f
			found = true
		}
	}
	return target, found
}

// CallFloors returns sorted list of calls.
func (l *ElevatorLogic) CallFloors() []int {
	var floors []int
	for f := range l.Calls {
		floors = append(floors, f)
	}
	sort.Ints(floors)
	return floors
}
