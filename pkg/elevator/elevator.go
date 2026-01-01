// Package elevator implements a concurrent, event-driven elevator simulator.
// 이 패키지는 스레드 안전(Thread-safe)한 이벤트 기반 엘리베이터 시뮬레이터를 구현합니다.
// 도메인 로직은 ElevatorLogic에 위임하고, 여기서는 실행 환경(Concurrency, Time, Events)을 관리합니다.
package elevator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// EventType represents the category of an elevator event.
// EventType는 엘리베이터 이벤트의 카테고리를 나타냅니다.
type EventType string

const (
	EventFloorChange     EventType = "FloorChange"
	EventDoorChange      EventType = "DoorChange"
	EventModeChange      EventType = "ModeChange"
	EventDirectionChange EventType = "DirectionChange"
	EventArrived         EventType = "Arrived"
	EventError           EventType = "Error"
)

// Event carries the state change information.
// Event는 시스템 내에서 발생한 상태 변화 정보를 담고 있습니다.
type Event struct {
	Type      EventType
	Payload   interface{}
	Timestamp time.Time
}

// DoorChangePayload carries detail for door events.
// DoorChangePayload는 문 이벤트의 세부 정보를 담고 있습니다.
type DoorChangePayload struct {
	Side  DoorSide
	State DoorState
}

// ArrivedPayload carries detail for arrival events.
// ArrivedPayload는 도착 이벤트의 세부 정보를 담고 있습니다.
type ArrivedPayload struct {
	Floor        int
	OpenDoorSide DoorSide
}

// OperationMode defines the control strategy of the elevator.
// OperationMode는 엘리베이터의 운행 모드를 정의합니다.
type OperationMode int

const (
	ModeAuto      OperationMode = iota // 자동 운행 (기본)
	ModeManual                         // 수동 제어 (점검 등)
	ModeMoving                         // 이사 모드 (장시간 문 열림 유지)
	ModeEmergency                      // 비상 정지 (모든 동작 즉시 중단)
)

func (m OperationMode) String() string {
	return [...]string{"Auto", "Manual", "Moving", "Emergency"}[m]
}

// Config holds immutable configuration parameters.
// Config는 시스템 시작 시 설정되며, 런타임 중에 변경되지 않습니다.
type Config struct {
	ID             string
	TravelTime     time.Duration       // 한 층 이동 시간 - 주행 속도
	TravelTimeEdge time.Duration       // 한 층 이동 시간 - 시작/정지 속도
	DoorSpeed      time.Duration       // 문 열림/닫힘 속도
	DoorOpenTime   time.Duration       // 층 도착 후 문 열림 유지 시간
	DoorReopenTime time.Duration       // 버튼 조작 후 문 열림 유지 시간
	InitialFloor   int                 // 초기 층 - 연속 인덱스
	MinFloor       int                 // 최저 층 인덱스
	MaxFloor       int                 // 최고 층 인덱스
	MaxWeight      int                 // 최대 허용 무게 kg
	FloorConfigs   map[int]FloorConfig // 층 정보
}

// Elevator is the Application Service.
// It orchestrates Logic, Time, and Concurrency.
type Elevator struct {
	mu     sync.RWMutex
	Config Config
	Logic  *ElevatorLogic

	// --- Runtime State ---
	Mode         OperationMode
	openWaitTime time.Duration

	// --- Loop Control ---
	doorTimer *time.Timer

	// --- Observability ---
	logger            *slog.Logger
	eventCh           chan Event
	droppedEventCount uint64

	// --- Internal Flags ---
	isOpenButtonPressed bool
}

// New initializes a new Elevator instance.
func New(config Config) (*Elevator, error) {
	// LogicConfig Init
	logicConfig := LogicConfig{
		MinFloor:     config.MinFloor,
		MaxFloor:     config.MaxFloor,
		InitialFloor: config.InitialFloor,
		MaxWeight:    config.MaxWeight,
		FloorConfigs: config.FloorConfigs,
	}

	// Logic Instance
	logic := NewElevatorLogic(logicConfig)

	// Validate (Fail Fast) - logic.AddCall checks range, but we check config sanity here.
	if config.MinFloor > config.MaxFloor {
		return nil, fmt.Errorf("invalid config: MinFloor (%d) > MaxFloor (%d)", config.MinFloor, config.MaxFloor)
	}

	if config.DoorReopenTime == 0 {
		config.DoorReopenTime = config.DoorOpenTime
	}

	e := &Elevator{
		Config:       config,
		Logic:        logic,
		Mode:         ModeAuto,
		doorTimer:    time.NewTimer(0),
		eventCh:      make(chan Event, 1000),
		logger:       slog.Default().With("id", config.ID),
		openWaitTime: config.DoorOpenTime,
	}

	// Stop timer initially
	if !e.doorTimer.Stop() {
		select {
		case <-e.doorTimer.C:
		default:
		}
	}

	e.logger.Info("Elevator Service initialized",
		"min", config.MinFloor,
		"max", config.MaxFloor,
		"init_floor", config.InitialFloor,
	)

	return e, nil
}

// Public API delegations -----------------------------------------------------

func (e *Elevator) Lock() {
	e.mu.Lock()
}

func (e *Elevator) Unlock() {
	e.mu.Unlock()
}

func (e *Elevator) Floor() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.Logic.Floor
}

func (e *Elevator) Direction() Direction {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.Logic.Direction
}

func (e *Elevator) Doors() map[DoorSide]DoorState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	// Deep copy
	d := make(map[DoorSide]DoorState)
	for k, v := range e.Logic.Doors {
		d[k] = v
	}
	return d
}

func (e *Elevator) Door(side DoorSide) DoorState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.Logic.Doors[side]
}

func (e *Elevator) Weight() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.Logic.Weight
}

func (e *Elevator) DroppedEventCount() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.droppedEventCount
}

func (e *Elevator) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logger.Info("Resetting elevator state")

	// Create new clean logic
	e.Logic = NewElevatorLogic(e.Logic.Config)

	e.publishEvent(EventFloorChange, e.Logic.Floor)
	e.publishEvent(EventDirectionChange, e.Logic.Direction)
	e.publishEvent(EventDoorChange, DoorChangePayload{Side: Front, State: DoorClose})
	e.publishEvent(EventDoorChange, DoorChangePayload{Side: Rear, State: DoorClose})
}

func (e *Elevator) CallFloors() []int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.Logic.CallFloors()
}

func (e *Elevator) Events() <-chan Event {
	return e.eventCh
}

func (e *Elevator) AddCall(floor int, isCarCall bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	err := e.Logic.AddCall(floor)
	if err != nil {
		e.logger.Warn("AddCall failed", "floor", floor, "err", err)
		return err
	}

	callType := "Hall"
	if isCarCall {
		callType = "Car"
	}
	e.logger.Info(callType+" Call registered", "floor", floor)
	return nil
}

func (e *Elevator) RemoveCall(floor int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Logic.RemoveCall(floor)
	e.logger.Debug("Call removed", "floor", floor)
}

func (e *Elevator) ClearCalls() {
	e.mu.Lock()
	defer e.mu.Unlock()
	// New logic instance or just clear map? logic doesn't have ClearCalls.
	// But logic.Calls is map, we can re-make it.
	// But direct access is risky if logic changes.
	// Let's iterate and delete? Or just replace the map.
	// For now adding method to Logic is best but I can't edit domain.go right now.
	// Accessing field directly since same package.
	e.Logic.Calls = make(map[int]bool)
	e.logger.Info("All calls cleared")
}

func (e *Elevator) CurrentState() (int, Direction, map[DoorSide]DoorState, int) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	doors := make(map[DoorSide]DoorState)
	for k, v := range e.Logic.Doors {
		doors[k] = v
	}
	return e.Logic.Floor, e.Logic.Direction, doors, e.Logic.Weight
}

func (e *Elevator) AddWeight(w int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Logic.Weight += w
	e.logger.Info("Weight added", "weight", e.Logic.Weight)
}

// PressOpenButton signals that the open button is pressed.
func (e *Elevator) PressOpenButton() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.isOpenButtonPressed = true
	e.logger.Debug("Open Button Pressed")

	// If door is closing, reopen immediately
	if e.Logic.Doors[Front] == DoorClosing || e.Logic.Doors[Rear] == DoorClosing {
		e.setDoor(Front, DoorOpening)
		e.setDoor(Rear, DoorOpening)
		// Reset timer for reopening logic (handled in step/timeout) or explicit here?
		// handleDoorTimeout checks state. If we set to Opening, next timeout will switch to Open.
		e.doorTimer.Reset(e.Config.DoorSpeed)
	} else if e.Logic.Doors[Front] == DoorOpen {
		// Extend hold time
		e.doorTimer.Reset(e.Config.DoorReopenTime)
	}
}

// ReleaseOpenButton signals that the open button is released.
func (e *Elevator) ReleaseOpenButton() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.isOpenButtonPressed = false
	e.logger.Debug("Open Button Released")
	// If doors are open, the timer is supposedly running or checked in handleDoorTimeout.
	// We rely on the loop checking the flag.
}

// PressCloseButton signals that the close button is pressed.
func (e *Elevator) PressCloseButton() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logger.Debug("Close Button Pressed")

	// Only effective if doors are open and safe to close
	if (e.Logic.Doors[Front] == DoorOpen || e.Logic.Doors[Rear] == DoorOpen) && !e.isOpenButtonPressed {
		// Close immediately (shorten timer)
		e.doorTimer.Reset(1 * time.Millisecond) // Trigger timeout almost immediately
	}
}

func (e *Elevator) SetDoor(side DoorSide, state DoorState) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.Logic.Doors[side] != state {
		e.Logic.SetDoor(side, state)
		e.publishEvent(EventDoorChange, DoorChangePayload{Side: side, State: state})
		e.logger.Info("Manual Door state set", "side", side, "state", state)
	}
}

func (e *Elevator) SetMode(mode OperationMode) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.Mode == mode {
		return
	}

	e.logger.Info("Operation Mode Changed", "from", e.Mode, "to", mode)
	e.Mode = mode
	e.publishEvent(EventModeChange, mode)

	if mode == ModeEmergency {
		e.logger.Warn("Emergency Stop Activated")
		e.doorTimer.Stop()
		e.setDirection(DirNone) // Updates logic and publishes event
	}
}

// Private helpers (State Updates & Events) -----------------------------------

func (e *Elevator) publishEvent(eventType EventType, payload interface{}) {
	event := Event{
		Type:      eventType,
		Payload:   payload,
		Timestamp: time.Now(),
	}

	select {
	case e.eventCh <- event:
	default:
		e.droppedEventCount++
		if e.droppedEventCount%100 == 1 {
			e.logger.Error("Event Channel Saturated", "dropped", e.droppedEventCount)
		}
	}
}

func (e *Elevator) setDirection(d Direction) {
	if e.Logic.Direction != d {
		e.Logic.SetDirection(d)
		e.publishEvent(EventDirectionChange, d)
	}
}

func (e *Elevator) setFloor(f int) {
	if e.Logic.Floor != f {
		e.Logic.SetFloor(f)
		e.publishEvent(EventFloorChange, f)
	}
}

func (e *Elevator) setDoor(side DoorSide, state DoorState) {
	if e.Logic.Doors[side] != state {
		e.Logic.SetDoor(side, state)
		e.publishEvent(EventDoorChange, DoorChangePayload{Side: side, State: state})
	}
}

// Engine Loop ----------------------------------------------------------------

func (e *Elevator) Run(ctx context.Context) error {
	e.logger.Info("Elevator Engine Started")
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	defer e.doorTimer.Stop()

	travelTimer := time.NewTimer(e.Config.TravelTime)
	travelTimer.Stop()

	isMoving := false

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("Engine Stopping")
			return ctx.Err()
		case <-ticker.C:
			e.step(&isMoving, travelTimer)
		case <-travelTimer.C:
			// Movement done
			shouldContinue, duration := e.handleMoveComplete()
			if shouldContinue {
				travelTimer.Reset(duration)
			} else {
				isMoving = false
				e.logger.Info("Travel timer stopped/completed")
			}
		case <-e.doorTimer.C:
			e.handleDoorTimeout()
		}
	}
}

// step calls DecidNextStep from Logic and enacts the result.
func (e *Elevator) step(isMoving *bool, travelTimer *time.Timer) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.Mode != ModeAuto {
		return
	}
	if *isMoving {
		return
	}

	action := e.Logic.DecideNextStep()

	switch action.Type {
	case ActionMove:
		// Logic decided to move.
		// Update Direction
		e.setDirection(action.Dir)

		target := action.Target
		// Calculate Duration (TravelTime)
		// For simplicity, naive fixed duration for now.
		// Real elevator might need checking if it's 1 floor or more?
		// Logic returns Target, but moving is step by step.
		// Wait, logic says "Move towards Target".
		// We initiate move to (Current + 1) or (Current - 1).

		// Logic action tells us "Go to strict Target or Move in Dir?"
		// My Logic impl: "ActionMove, Dir=Up, Target=8".
		// So we start moving.

		duration := e.Config.TravelTime

		*isMoving = true
		if !travelTimer.Stop() {
			select {
			case <-travelTimer.C:
			default:
			}
		}
		travelTimer.Reset(duration)

		e.logger.Debug("Started Moving", "dir", action.Dir, "target", target)

	case ActionStop:
		// Logic decided to stop (idle).
		if e.Logic.Direction != DirNone {
			e.setDirection(DirNone)
		}

	case ActionOpenDoor:
		// Arrived at target or already at target.
		e.handleArrival(action.Target) // Opens door, clears call

	case ActionNone:
		// Do nothing
	}
}

func (e *Elevator) handleMoveComplete() (bool, time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 1. Physically move 1 floor
	newFloor := e.Logic.Floor
	if e.Logic.Direction == DirUp {
		newFloor++
	} else if e.Logic.Direction == DirDown {
		newFloor--
	}
	e.setFloor(newFloor)

	// 2. Ask logic what to do next *at this floor*
	// Logic might say "Open Door" (if call exists here) or "Continue Move".

	// We check logic *again*.
	// But Logic.DecideNextStep will see we are at newFloor.
	action := e.Logic.DecideNextStep()

	switch action.Type {
	case ActionOpenDoor:
		// We should stop here.
		e.handleArrival(e.Logic.Floor)
		return false, 0

	case ActionMove:
		// Continue moving
		// Check if direction changed?
		if action.Dir != e.Logic.Direction {
			// Change direction -> Stop first?
			// Simplified: Update dir and continue.
			e.setDirection(action.Dir)
		}
		return true, e.Config.TravelTime

	default:
		// ActionNone or Stop -> Stop
		e.setDirection(DirNone)
		return false, 0
	}
}

func (e *Elevator) handleArrival(floor int) {
	e.logger.Info("Arrived at floor", "floor", floor)

	// Determine Open Side from Config
	openSide := Front // Default
	if cfg, ok := e.Config.FloorConfigs[floor]; ok {
		openSide = cfg.OpenDoorSide
	}

	// Update Doors
	if openSide&Front != 0 {
		e.setDoor(Front, DoorOpening)
	}
	if openSide&Rear != 0 {
		e.setDoor(Rear, DoorOpening)
	}

	// Remove Call logic
	e.Logic.RemoveCall(floor)

	// Publish Arrived
	e.publishEvent(EventArrived, ArrivedPayload{
		Floor:        floor,
		OpenDoorSide: openSide,
	})

	// Start Door Timer (Wait for full open)
	// Actually typical flow: Opening -> (Time?) -> Open -> (Wait) -> Closing.
	// My simulator assumes Opening is instant or handled by timer?
	// The original code set timer for 'DoorSpeed'.

	e.openWaitTime = e.Config.DoorOpenTime
	e.doorTimer.Reset(e.Config.DoorSpeed)
}

func (e *Elevator) handleDoorTimeout() {
	e.mu.Lock()
	defer e.mu.Unlock()

	state := e.Logic.Doors[Front]
	if state == DoorClose {
		state = e.Logic.Doors[Rear]
	}

	switch state {
	case DoorOpening:
		// Transition to Open
		if e.Logic.Doors[Front] == DoorOpening {
			e.setDoor(Front, DoorOpen)
		}
		if e.Logic.Doors[Rear] == DoorOpening {
			e.setDoor(Rear, DoorOpen)
		}
		// Hold for openWaitTime
		e.logger.Info("Doors OPEN", "hold", e.openWaitTime)
		e.doorTimer.Reset(e.openWaitTime)

	case DoorOpen:
		// Try to close
		// Check overload
		if e.Config.MaxWeight > 0 && e.Logic.Weight > e.Config.MaxWeight {
			e.logger.Warn("Overloaded, holding doors")
			e.doorTimer.Reset(e.openWaitTime)
			return
		}

		// Check button (isOpenButtonPressed)
		if e.isOpenButtonPressed {
			e.logger.Debug("Button pressed, holding doors")
			e.doorTimer.Reset(e.Config.DoorReopenTime)
			return
		}

		// Close
		if e.Logic.Doors[Front] == DoorOpen {
			e.setDoor(Front, DoorClosing)
		}
		if e.Logic.Doors[Rear] == DoorOpen {
			e.setDoor(Rear, DoorClosing)
		}
		e.doorTimer.Reset(e.Config.DoorSpeed)

	case DoorClosing:
		// Transition to Close
		e.setDoor(Front, DoorClose)
		e.setDoor(Rear, DoorClose)
		e.logger.Info("Doors Closed")
		// Triggers run loop to move if needed
	}
}
