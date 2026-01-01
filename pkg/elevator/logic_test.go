package elevator

import (
	"testing"
)

func TestElevatorLogic_Init(t *testing.T) {
	cfg := LogicConfig{
		MinFloor:     1,
		MaxFloor:     10,
		InitialFloor: 1,
	}
	logic := NewElevatorLogic(cfg)

	if logic.Floor != 1 {
		t.Errorf("Expected initial floor 1, got %d", logic.Floor)
	}
	if logic.Direction != DirNone {
		t.Errorf("Expected initial direction None, got %s", logic.Direction)
	}
	if logic.Doors[Front] != DoorClose {
		t.Errorf("Expected front door closed, got %s", logic.Doors[Front])
	}
}

func TestElevatorLogic_AddCall(t *testing.T) {
	cfg := LogicConfig{
		MinFloor:     1,
		MaxFloor:     5,
		InitialFloor: 1,
	}
	logic := NewElevatorLogic(cfg)

	// Valid Call
	err := logic.AddCall(3)
	if err != nil {
		t.Errorf("Failed to add valid call: %v", err)
	}
	if !logic.Calls[3] {
		t.Errorf("Call at 3 not registered")
	}

	// Invalid Call (Out of range)
	err = logic.AddCall(6)
	if err == nil {
		t.Error("Expected error for out-of-range call, got nil")
	}

	// Invalid Call (Inaccessible - explicit config)
	cfg.FloorConfigs = map[int]FloorConfig{
		2: {FloorNumber: 2, IsAccessible: false},
	}
	logic = NewElevatorLogic(cfg)
	err = logic.AddCall(2)
	if err == nil {
		t.Error("Expected error for inaccessible floor, got nil")
	}
}

func TestElevatorLogic_DecideNextStep_SCAN(t *testing.T) {
	cfg := LogicConfig{
		MinFloor:     1,
		MaxFloor:     10,
		InitialFloor: 5,
	}
	logic := NewElevatorLogic(cfg)

	// Scenario 1: Idle, Call above -> Move Up
	logic.AddCall(8)
	action := logic.DecideNextStep()
	if action.Type != ActionMove || action.Dir != DirUp || action.Target != 8 {
		t.Errorf("Scenario 1 failed: Expected Move Up to 8, got %v", action)
	}

	// Scenario 2: Moving Up, Call above and below -> Continue Up (SCAN)
	logic = NewElevatorLogic(cfg) // Reset
	logic.Floor = 5
	logic.Direction = DirUp
	logic.AddCall(2) // Below
	logic.AddCall(9) // Above
	action = logic.DecideNextStep()
	if action.Type != ActionMove || action.Dir != DirUp || action.Target != 9 {
		t.Errorf("Scenario 2 failed: Expected Move Up to 9, got %v", action)
	}

	// Scenario 3: Moving Up, No call above, Call below -> Reversal (Move Down via Stop/Calc)
	// In strict SCAN, if no calls in current direction, it checks other direction.
	// Logic implementation handles this.
	logic = NewElevatorLogic(cfg)
	logic.Direction = DirUp
	logic.Calls = make(map[int]bool)
	logic.AddCall(2)

	action = logic.DecideNextStep()
	// Depending on implementation, it might return Move Down directly OR Stop first.
	// Current impl: Phase 2 selects nearest call (2). Logic checks target (2) < current (5) -> DirDown.
	if action.Type != ActionMove || action.Dir != DirDown || action.Target != 2 {
		t.Errorf("Scenario 3 failed: Expected Move Down to 2 (Reversal), got %v", action)
	}

	// Scenario 4: Arrived at Target -> Open Door
	logic = NewElevatorLogic(cfg) // Floor 5
	logic.AddCall(5)
	action = logic.DecideNextStep()
	if action.Type != ActionOpenDoor || action.Target != 5 {
		t.Errorf("Scenario 4 failed: Expected OpenDoor at 5, got %v", action)
	}
}

func TestElevatorLogic_Doors(t *testing.T) {
	cfg := LogicConfig{MinFloor: 1, MaxFloor: 5, InitialFloor: 1}
	logic := NewElevatorLogic(cfg)

	// Doors Open -> No Move
	logic.Doors[Front] = DoorOpen
	logic.AddCall(3)
	action := logic.DecideNextStep()
	if action.Type != ActionNone {
		t.Errorf("Expected ActionNone when doors are open, got %v", action)
	}
}
