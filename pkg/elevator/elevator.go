// Package elevator implements a concurrent, event-driven elevator simulator.
// 이 패키지는 스레드 안전(Thread-safe)한 이벤트 기반 엘리베이터 시뮬레이터를 구현합니다.
// SCAN 알고리즘을 사용하여 효율적인 층별 이동을 제어합니다.
package elevator

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
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

// FloorConfig holds specific settings for a single floor.
// FloorConfig는 단일 층의 특정 설정을 저장합니다.
type FloorConfig struct {
	FloorNumber  int      // 층 번호
	IsAccessible bool     // 접근 가능 여부
	OpenDoorSide DoorSide // 해당 층 도착시 문 열림 방향
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

// Elevator is the core logic engine.
// Elevator는 모든 상태 변경은 Mutex로 보호되며, 변경 사항은 Event 채널로 전파됩니다.
type Elevator struct {
	mu     sync.RWMutex
	Config Config

	// --- State (가변 상태) ---
	Mode         OperationMode          // 운행 모드
	floor        int                    // 현재 층 - 연속 인덱스
	direction    Direction              // 운행 방향
	doors        map[DoorSide]DoorState // 문 상태
	weight       int                    // 현재 무게
	openWaitTime time.Duration          // 상황에 따른 열림 대기 시간

	// --- Queue (호출 저장소) ---
	callFloors map[int]bool // 호출된 층 집합

	// --- Loop Control ---
	doorTimer *time.Timer // 문 열림/닫힘 제어 타이머

	// --- Observability ---
	logger            *slog.Logger
	eventCh           chan Event // 외부 통신용 이벤트 채널
	droppedEventCount uint64     // 버퍼 오버플로우로 버려진 이벤트 수

	// --- Internal Flags ---
	isOpenButtonPressed bool // 열림 버튼이 눌러졌는지 여부
}

// New initializes a new Elevator instance with strict validation.
// 잘못된 설정(예: Min > Max)이 감지되면 즉시 에러를 반환합니다 (Fail Fast).
func New(config Config) (*Elevator, error) {
	// Defensive Initialization: MinFloor > MaxFloor이면 즉시 에러 반환
	if config.MinFloor > config.MaxFloor {
		return nil, fmt.Errorf("invalid config: MinFloor (%d) > MaxFloor (%d)", config.MinFloor, config.MaxFloor)
	}

	// Defensive Initialization: FloorConfigs가 nil이면 초기화
	if config.FloorConfigs == nil {
		config.FloorConfigs = make(map[int]FloorConfig)
	}

	// 누락된 층 설정은 기본값으로 채움
	for i := config.MinFloor; i <= config.MaxFloor; i++ {
		if _, ok := config.FloorConfigs[i]; !ok {
			config.FloorConfigs[i] = FloorConfig{
				FloorNumber:  i,
				IsAccessible: true,
				OpenDoorSide: Front,
			}
		}
	}

	// DoorReopenTime 기본값 보정
	if config.DoorReopenTime == 0 {
		config.DoorReopenTime = config.DoorOpenTime
	}

	e := &Elevator{
		Config:    config,
		Mode:      ModeAuto,
		floor:     config.InitialFloor,
		direction: DirNone,
		doors: map[DoorSide]DoorState{
			Front: DoorClose,
			Rear:  DoorClose,
		},
		callFloors:   make(map[int]bool),
		doorTimer:    time.NewTimer(0),
		eventCh:      make(chan Event, 1000), // Increased buffer for safety (안전성을 위해 버퍼 증대)
		logger:       slog.Default().With("id", config.ID),
		openWaitTime: config.DoorOpenTime,
	}

	// 생성 시 타이머는 Stop 상태로 시작 (명시적 Drain 처리 불필요하지만 안전을 위해)
	if !e.doorTimer.Stop() {
		select {
		case <-e.doorTimer.C:
		default:
		}
	}

	e.logger.Info("Elevator initialized",
		"min", config.MinFloor,
		"max", config.MaxFloor,
		"init_floor", config.InitialFloor,
	)

	return e, nil
}

// Lock manually locks the elevator state.
// Lock은 엘리베이터 뮤텍스를 잠금합니다.
func (e *Elevator) Lock() {
	e.mu.Lock()
}

// Unlock manually unlocks the elevator state.
// Unlock은 엘리베이터 뮤텍스를 잠금합니다.
func (e *Elevator) Unlock() {
	e.mu.Unlock()
}

// Floor returns the current floor safely.
// Floor은 현재 층을 안전하게 반환합니다.
func (e *Elevator) Floor() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.floor
}

// Direction returns the current direction safely.
// Direction은 현재 방향을 안전하게 반환합니다.
func (e *Elevator) Direction() Direction {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.direction
}

// Doors returns a snapshot of door states.
// Doors는 문 상태의 복사본을 안전하게 반환합니다.
func (e *Elevator) Doors() map[DoorSide]DoorState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	d := make(map[DoorSide]DoorState)
	for k, v := range e.doors {
		d[k] = v
	}
	return d
}

// Door returns the state of a specific door safely.
// Door는 특정 문의 상태를 안전하게 반환합니다.
func (e *Elevator) Door(side DoorSide) DoorState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.doors[side]
}

// Weight returns the current payload weight.
// Weight는 현재 무게을 안전하게 반환합니다.
func (e *Elevator) Weight() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.weight
}

// DroppedEventCount returns diagnostic metric for channel health.
// DroppedEventCount는 버퍼 오버플로우로 버려진 이벤트 수를 안전하게 반환합니다.
func (e *Elevator) DroppedEventCount() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.droppedEventCount
}

// Reset clears the call queue and resets state (Initialization, Emergency Recovery).
// Reset은 엘리베이터 상태를 초기화하거나 복구하는 데 사용됩니다.
func (e *Elevator) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logger.Info("Resetting elevator state")
	e.callFloors = make(map[int]bool)
	e.setDirection(DirNone)
	e.setDoor(Front, DoorClose)
	e.setDoor(Rear, DoorClose)
}

// CallFloors returns a sorted list of pending target floors.
// CallFloors는 대기 중인 목표 층의 정렬된 리스트를 반환합니다.
func (e *Elevator) CallFloors() []int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var floors []int
	for f := range e.callFloors {
		floors = append(floors, f)
	}
	sort.Ints(floors)
	return floors
}

// Events returns the read-only channel for state change notifications.
// Events는 상태 변경 알림을 위한 읽기 전용 채널을 반환합니다.
func (e *Elevator) Events() <-chan Event {
	return e.eventCh
}

// publishEvent sends an event to the channel without blocking logic.
// 채널이 가득 차면 이벤트를 버리고 메트릭을 증가시킵니다 (System Stability).
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
		// Log rarely to avoid disk I/O flooding
		if e.droppedEventCount%100 == 1 {
			e.logger.Error("Event Channel Saturated", "dropped", e.droppedEventCount, "type", eventType)
		}
	}
}

// setFloor updates the floor and publishes an event.
// setFloor는 층을 업데이트하고 이벤트를 게시합니다.
func (e *Elevator) setFloor(f int) {
	if e.floor != f {
		e.floor = f
		e.publishEvent(EventFloorChange, f)
	}
}

// setDirection updates the direction and publishes an event.
// setDirection는 방향을 업데이트하고 이벤트를 게시합니다.
func (e *Elevator) setDirection(d Direction) {
	if e.direction != d {
		e.direction = d
		e.publishEvent(EventDirectionChange, d)
	}
}

// setDoor updates the door state and publishes an event.
// setDoor는 문 상태를 업데이트하고 이벤트를 게시합니다.
func (e *Elevator) setDoor(side DoorSide, state DoorState) {
	if e.doors[side] != state {
		e.doors[side] = state
		e.publishEvent(EventDoorChange, DoorChangePayload{Side: side, State: state})
	}
}

// SetMode changes the operation mode.
// SetMode는 운행 모드를 변경합니다.
func (e *Elevator) SetMode(mode OperationMode) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.Mode == mode {
		return
	}

	e.logger.Info("Operation Mode Changed", "from", e.Mode, "to", mode)
	e.Mode = mode
	e.publishEvent(EventModeChange, mode)

	// Emergency stop
	if mode == ModeEmergency {
		e.logger.Warn("Emergency Stop Activated")
		e.doorTimer.Stop()
		e.direction = DirNone
		// Note: Moving timer in Run loop is handled by checking isMoving logic or needs explicit stop channel if required immediately.
		// For now simple state update.
	}
}

// AddCall registers a new destination floor.
// 유효하지 않은 층이나 접근 불가능한 층은 거부됩니다.
func (e *Elevator) AddCall(floor int, isCarCall bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 층 범위 확인
	if floor < e.Config.MinFloor || floor > e.Config.MaxFloor {
		e.logger.Warn("AddCall failed: floor out of range",
			"floor", floor, "min", e.Config.MinFloor, "max", e.Config.MaxFloor)
		return fmt.Errorf("floor %d out of range", floor)
	}

	// 접근 가능성 확인
	cfg := e.Config.FloorConfigs[floor] // Safe as we init in New
	if !cfg.IsAccessible {
		e.logger.Warn("AddCall failed: inaccessible floor", "floor", floor)
		return fmt.Errorf("floor %d is inaccessible", floor)
	}

	// 이미 등록된 호출인지 확인
	if e.callFloors[floor] {
		e.logger.Debug("Call already registered", "floor", floor)
		return nil
	}

	e.callFloors[floor] = true

	callType := "Hall"
	if isCarCall {
		callType = "Car"
	}
	e.logger.Info(callType+" Call registered", "floor", floor)
	return nil
}

// RemoveCall cancels a pending call manually.
// RemoveCall은 대기 중인 호출을 수동으로 취소합니다.
func (e *Elevator) RemoveCall(floor int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logger.Debug("Call removed", "floor", floor)
	delete(e.callFloors, floor)
}

// ClearCalls removes all pending calls.
// ClearCalls은 모든 대기 중인 호출을 제거합니다.
func (e *Elevator) ClearCalls() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.callFloors = make(map[int]bool)
	e.logger.Info("All calls cleared")
}

// CurrentState returns a complete snapshot of the elevator status.
// CurrentState는 엘리베이터의 전체 상태 스냅샷을 반환합니다.
func (e *Elevator) CurrentState() (int, Direction, map[DoorSide]DoorState, int) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	// Deep copy doors
	doors := make(map[DoorSide]DoorState)
	for k, v := range e.doors {
		doors[k] = v
	}
	return e.floor, e.direction, doors, e.weight
}

// AddWeight simulates passenger boarding/alighting.
// AddWeight는 승객의 탑승/하차를 시뮬레이션합니다.
func (e *Elevator) AddWeight(w int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.weight += w
	e.logger.Info("Weight added", "weight", e.weight)
}

// SetDoor manually overrides door state (Use with caution).
// SetDoor는 문 상태를 수동으로 재정의합니다. (주의를 기울여 사용).
func (e *Elevator) SetDoor(side DoorSide, state DoorState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.setDoor(side, state)
	e.logger.Info("Manual Door state set", "side", side, "state", state)
}

// Run executes the main event loop.
// It manages movement scheduling and door state transitions.
// Run은 엘리베이터의 메인 이벤트 루프를 실행합니다.
// 스케쥴 관리와 문 상태 전환을 관리합니다.
func (e *Elevator) Run(ctx context.Context) error {
	e.logger.Info("Elevator Engine Started")

	// Polling ticker for next-step calculation
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// Ensure doorTimer is cleaned up
	defer e.doorTimer.Stop()

	// Travel timer manages the time it takes to move between floors
	travelTimer := time.NewTimer(e.Config.TravelTime)
	travelTimer.Stop() // Ensure timer is stopped before use
	defer func() {
		if !travelTimer.Stop() {
			select {
			case <-travelTimer.C:
			default:
			}
		}
	}()

	isMoving := false

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("Engine Stopping (Context Cancelled)")
			return ctx.Err()

		case <-ticker.C:
			// Step the elevator logic
			e.step(&isMoving, travelTimer)

		case <-travelTimer.C:
			// Travel timer expired
			shouldContinue, duration := e.handleMove()
			if shouldContinue {
				// Reset travel timer
				travelTimer.Reset(duration)
			} else {
				// Travel completed
				isMoving = false
				e.logger.Info("Travel completed")
			}

		case <-e.doorTimer.C:
			// Door timer expired
			e.handleDoorTimeout()
		}
	}
}

// step evaluates the current state and determines the next action.
// Called every tick.
// step은 현재 상태를 평가하고 다음 동작을 결정합니다.
// 매 틱마다 호출됩니다.
func (e *Elevator) step(isMoving *bool, travelTimer *time.Timer) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// [Guard Clause] Auto 모드일 때만 자동 운행 로직 수행
	if e.Mode != ModeAuto {
		return
	}

	// [Guard Clause] 이미 이동 중이면 타이머 만료를 대기 (중복 실행 방지)
	if *isMoving {
		return
	}

	// [Safety Guard] 문이 완전히 닫히지 않았으면 이동 불가
	for _, state := range e.doors {
		if state != DoorClose {
			return
		}
	}

	// 목표 층 탐색 (SCAN 알고리즘)
	target, found := e.selectNextTarget()
	if !found {
		if e.direction != DirNone {
			e.logger.Debug("💤 Idle State (No calls)", "floor", e.floor)
			e.direction = DirNone // 대기 상태로 전환
		}
		return
	}

	// 이동 방향 및 시간 계산
	var duration time.Duration
	var nextDir Direction

	if target > e.floor {
		nextDir = DirUp
	} else if target < e.floor {
		nextDir = DirDown
	} else {
		// 현재 층이 목표인 경우 (즉시 도착 처리)
		e.handleArrival(target)
		return
	}

	// 로그 노이즈 감소: 방향이 바뀔 때만 중요 로그 출력
	if e.direction != nextDir {
		e.logger.Info("🧭 Direction Changed", "new_dir", nextDir, "target", target)
	} else {
		e.logger.Debug("🚅 Moving", "dir", nextDir, "target", target)
	}

	duration = e.getNextMoveDuration(target)
	e.setDirection(nextDir)

	// 타이머 안전하게 시작
	*isMoving = true
	if !travelTimer.Stop() {
		select {
		case <-travelTimer.C:
		default:
		}
	}
	travelTimer.Reset(duration)
}

// handleMove processes the completion of a single floor movement.
// Returns: (shouldContinue, nextDuration)
func (e *Elevator) handleMove() (bool, time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 물리적 위치 업데이트
	switch e.direction {
	case DirUp:
		e.setFloor(e.floor + 1)
	case DirDown:
		e.setFloor(e.floor - 1)
	}

	// 현재 층이 호출 목록에 있는지 확인
	if e.callFloors[e.floor] {
		// 호출이 있는 경우 정지
		e.logger.Info("Stopping at floor (Call found)", "floor", e.floor)
		e.handleArrival(e.floor)
		return false, 0
	}

	// 더 이동해야 하는지 확인 (Look Ahead)
	target, found := e.selectNextTarget()
	if !found {
		// 더 이상 갈 곳이 없음
		e.setDirection(DirNone)
		return false, 0
	}

	// 방향 유지 여부 결정
	// 가던 방향으로 계속 갈 수 있다면 멈추지 않고 이동 (Cruising)
	keepDirection := (e.direction == DirUp && target > e.floor) ||
		(e.direction == DirDown && target < e.floor)

	if keepDirection {
		return true, e.getNextMoveDuration(target)
	}

	// 방향을 바꿔야 한다면 일단 정지 (Stop to reverse)
	e.setDirection(DirNone)
	return false, 0
}

// selectNextTarget implements the SCAN (Elevator) Algorithm.
// 1. 현재 진행 방향(Heading)에 있는 호출을 우선 처리합니다.
// 2. 진행 방향에 호출이 없으면, 반대 방향의 가장 가까운 호출을 선택합니다.
func (e *Elevator) selectNextTarget() (int, bool) {
	if len(e.callFloors) == 0 {
		return 0, false
	}

	// Phase 1: Current Direction Scan
	// 현재 방향으로 계속 가면서 처리할 호출이 있는지 확인
	switch e.direction {
	case DirUp:
		minDist := math.MaxInt64
		target := -1
		found := false
		for f := range e.callFloors {
			if f > e.floor {
				dist := f - e.floor
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
		for f := range e.callFloors {
			if f < e.floor {
				dist := e.floor - f
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

	// Phase 2: Direction Reversal (Nearest Call)
	// 진행 방향에 호출이 없으므로, 가장 가까운 호출을 찾아 방향 전환
	minDist := math.MaxInt64
	target := -1
	found := false

	for f := range e.callFloors {
		dist := int(math.Abs(float64(f - e.floor)))
		if dist < minDist {
			minDist = dist
			target = f
			found = true
		}
	}
	if found {
		return target, true
	}
	return 0, false
}

// handleArrival executes arrival procedures: Open doors, Clear call.
// handleArrival은 층 도착 시 문 열기, 콜 제거, 핸들러 호출을 담당합니다.
func (e *Elevator) handleArrival(floor int) {
	e.logger.Info("Arrived at floor", "floor", floor)

	openDoorSide := Front
	cfg, found := e.getFloorConfig(floor)
	if found {
		openDoorSide = cfg.OpenDoorSide
	}

	// 문 열기 시작 (상태 전이: Closed -> Opening)
	if openDoorSide&Front != 0 {
		e.setDoor(Front, DoorOpening)
	}
	if openDoorSide&Rear != 0 {
		e.setDoor(Rear, DoorOpening)
	}

	// 콜 제거
	delete(e.callFloors, floor)

	// Publish Arrived event
	e.publishEvent(EventArrived, ArrivedPayload{
		Floor:        floor,
		OpenDoorSide: openDoorSide,
	})

	// 도착 후 대기 시간 설정
	e.openWaitTime = e.Config.DoorOpenTime

	if !e.doorTimer.Stop() {
		select {
		case <-e.doorTimer.C:
		default:
		}
	}
	e.doorTimer.Reset(e.Config.DoorSpeed)

}

// handleDoorTimeout manages the Door State Machine.
// Transitions: Opening -> Open -> Closing -> Closed
func (e *Elevator) handleDoorTimeout() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 현재 활성화된(움직이는) 문 상태 식별
	state := e.doors[Front]
	if state == DoorClose {
		state = e.doors[Rear]
	}
	if state == DoorClose {
		return // 문이 닫혀있으므로 타이머 이벤트 무시
	}

	switch state {
	case DoorOpening:
		// [State Transition] Opening -> Open
		// 문 열림 동작 완료. 이제 문을 열어두고 승객을 기다림.
		if e.doors[Front] == DoorOpening {
			e.setDoor(Front, DoorOpen)
		}
		if e.doors[Rear] == DoorOpening {
			e.setDoor(Rear, DoorOpen)
		}

		e.logger.Info("Doors are now fully OPEN", "hold_duration", e.openWaitTime)
		e.doorTimer.Reset(e.openWaitTime)

	case DoorOpen:
		// [State Check] 닫힘 조건 검사
		// 1. 열림 버튼이 눌려있는가?
		if e.isOpenButtonPressed {
			e.logger.Debug("Holding Doors (Button Pressed)")
			e.doorTimer.Reset(e.Config.DoorReopenTime)
			return
		}

		// 2. 최대 무게를 초과했는가?
		if e.Config.MaxWeight > 0 && e.weight > e.Config.MaxWeight {
			e.logger.Warn("Overloaded: Cannot Close Doors", "weight", e.weight)
			e.doorTimer.Reset(e.openWaitTime)
			return
		}

		// [State Transition] Open -> Closing
		// 대기 시간 종료. 문 닫기 시작.
		if e.doors[Front] == DoorOpen {
			e.setDoor(Front, DoorClosing)
		}
		if e.doors[Rear] == DoorOpen {
			e.setDoor(Rear, DoorClosing)
		}

		e.logger.Debug("Doors Closing")
		e.doorTimer.Reset(e.Config.DoorSpeed)

	case DoorClosing:
		// [State Transition] Closing -> Close
		// 문 닫힘 동작 완료.
		if e.doors[Front] == DoorClosing {
			e.setDoor(Front, DoorClose)
		}
		if e.doors[Rear] == DoorClosing {
			e.setDoor(Rear, DoorClose)
		}
		e.logger.Info("Doors are now fully CLOSED")
	}
}

// getNextMoveDuration determines travel speed.
// getNextMoveDuration은 다음 이동에 걸리는 시간을 계산합니다.
func (e *Elevator) getNextMoveDuration(target int) time.Duration {
	dist := int(math.Abs(float64(target - e.floor)))

	// 정지 상태에서 출발하거나(Start), 바로 다음 층에 멈춰야 하면(Stop)
	// 가감속 시간을 적용하여 부드럽게 이동 (TravelTimeEdge)
	if e.direction == DirNone || dist == 1 {
		return e.Config.TravelTimeEdge
	}

	// 일반적인 경우, 정상 속도로 이동 (TravelTime)
	return e.Config.TravelTime
}

// PressOpenButton handles user input: Open Button Pressed.
// PressOpenButton은 사용자 입력을 처리합니다: 열림 버튼을 누름.
func (e *Elevator) PressOpenButton() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.isOpenButtonPressed = true

	// 현재 활성화된 문 상태 확인 (Front 우선, 닫혀있으면 Rear 확인)
	state := e.doors[Front]
	if state == DoorClose {
		state = e.doors[Rear]
	}

	switch state {
	case DoorClosing:
		// 닫히다가 다시 열림 (Reopen)
		e.logger.Info("Open button pressed: Reopening doors", "state", state)
		// 닫히고 있는 문만 다시 엽니다.
		if e.doors[Front] == DoorClosing {
			e.setDoor(Front, DoorOpening)
		}
		if e.doors[Rear] == DoorClosing {
			e.setDoor(Rear, DoorOpening)
		}

		e.openWaitTime = e.Config.DoorReopenTime

		// 타이머 리셋 (문 여는 시간 소요)
		if !e.doorTimer.Stop() {
			select {
			case <-e.doorTimer.C:
			default:
			}
		}
		e.doorTimer.Reset(e.Config.DoorSpeed)

	case DoorOpen:
		// 이미 열려있음: 대기 시간 연장
		e.logger.Debug("Open Button: Extending Hold Time")
		e.openWaitTime = e.Config.DoorReopenTime
		if !e.doorTimer.Stop() {
			select {
			case <-e.doorTimer.C:
			default:
			}
		}
		e.doorTimer.Reset(e.Config.DoorReopenTime)

	case DoorClose:
		// 정지 상태이고 문이 닫혀있을 때 열림 버튼 누르면 문 열기
		if e.direction == DirNone {
			e.logger.Info("Open Button: Opening Doors from Idle")

			// 현재 층 설정 확인하여 열릴 문 결정 (없으면 Front)
			openSide := Front
			if cfg, ok := e.Config.FloorConfigs[e.floor]; ok {
				openSide = cfg.OpenDoorSide
			}

			if openSide&Front != 0 {
				e.setDoor(Front, DoorOpening)
			}
			if openSide&Rear != 0 {
				e.setDoor(Rear, DoorOpening)
			}

			e.openWaitTime = e.Config.DoorReopenTime

			// 타이머 시작 (Opening)
			if !e.doorTimer.Stop() {
				select {
				case <-e.doorTimer.C:
				default:
				}
			}
			e.doorTimer.Reset(e.Config.DoorSpeed)
		}
	}
}

// ReleaseOpenButton handles user input: Open Button Released.
// ReleaseOpenButton: 열림 버튼에서 손을 뗌 (이때부터 닫힘 카운트다운 시작)
func (e *Elevator) ReleaseOpenButton() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.isOpenButtonPressed = false
	e.logger.Debug("Open Button Released")

	// 문이 활짝 열려있다면, 손을 뗀 시점부터 카운트다운 다시 시작
	if e.doors[Front] == DoorOpen {
		e.openWaitTime = e.Config.DoorReopenTime
		if !e.doorTimer.Stop() {
			select {
			case <-e.doorTimer.C:
			default:
			}
		}
		e.doorTimer.Reset(e.Config.DoorReopenTime)
	}
}

// PressCloseButton handles user input: Close Button Pressed.
// PressCloseButton: 닫힘 버튼 클릭
func (e *Elevator) PressCloseButton() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// If open button is held, close button is ignored
	// 열림 버튼이 눌려있으면 닫힘 버튼은 무시됨 (우선순위)
	if e.isOpenButtonPressed {
		e.logger.Debug("Close button ignored: Open button is being held")
		return
	}

	if e.doors[Front] == DoorOpen {
		e.logger.Info("Close button pressed: Closing immediately")
		e.doorTimer.Reset(0) // 즉시 handleDoorTimeout 트리거
	}
}

// getFloorConfig returns the configuration for a specific floor.
// getFloorConfig는 특정 층의 설정을 반환합니다.
func (e *Elevator) getFloorConfig(floor int) (FloorConfig, bool) {
	cfg, ok := e.Config.FloorConfigs[floor]
	return cfg, ok
}
