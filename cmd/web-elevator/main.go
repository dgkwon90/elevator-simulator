package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"context"
	"go-elevator-simulator/pkg/elevator"
	"os"

	"github.com/gorilla/websocket"
)

//go:embed static/*
var staticFiles embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

// Message types
// 메시지 타입 정의
type ClientMessage struct {
	Action string          `json:"action"`
	Config *ElevatorConfig `json:"config,omitempty"`
	Floor  int             `json:"floor,omitempty"`
	Mode   int             `json:"mode,omitempty"`
	Weight int             `json:"weight,omitempty"`
}

type ElevatorConfig struct {
	ID             string  `json:"id"`
	MinFloor       int     `json:"minFloor"`
	MaxFloor       int     `json:"maxFloor"`
	InitialFloor   int     `json:"initialFloor"`
	TravelTime     float64 `json:"travelTime"`     // seconds
	DoorSpeed      float64 `json:"doorSpeed"`      // seconds
	DoorOpenTime   float64 `json:"doorOpenTime"`   // seconds
	DoorReopenTime float64 `json:"doorReopenTime"` // seconds (Time to keep door open after button press / 버튼 조작 후 문 열림 시간)
}

type ServerMessage struct {
	Type       string      `json:"type"`
	EventType  string      `json:"eventType,omitempty"`
	Payload    interface{} `json:"payload,omitempty"`
	Timestamp  string      `json:"timestamp,omitempty"`
	Floor      int         `json:"floor"`
	Direction  string      `json:"direction"`
	Doors      DoorStates  `json:"doors"`
	Mode       int         `json:"mode"`
	CallFloors []int       `json:"callFloors"`
	Weight     int         `json:"weight"`
	MaxWeight  int         `json:"maxWeight"`
}

type DoorStates struct {
	Front string `json:"front"`
	Rear  string `json:"rear"`
}

// ElevatorSession manages a WebSocket connection with an elevator instance
// ElevatorSession은 엘리베이터 인스턴스와의 WebSocket 연결을 관리합니다.
type ElevatorSession struct {
	conn     *websocket.Conn
	elevator *elevator.Elevator
	mu       sync.Mutex
	done     chan struct{}
	cancel   context.CancelFunc
}

func NewElevatorSession(conn *websocket.Conn) *ElevatorSession {
	return &ElevatorSession{
		conn: conn,
		done: make(chan struct{}),
	}
}

func (s *ElevatorSession) HandleMessages() {
	slog.Info("Session started", "remote_addr", s.conn.RemoteAddr())
	defer func() {
		close(s.done)
		if s.cancel != nil {
			s.cancel()
		}
		_ = s.conn.Close()
		slog.Info("Session ended", "remote_addr", s.conn.RemoteAddr())
	}()

	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Error("WebSocket read error", "error", err)
			}
			return
		}

		var msg ClientMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			slog.Warn("Failed to parse message", "error", err)
			continue
		}

		s.handleAction(msg)
	}
}

func (s *ElevatorSession) handleAction(msg ClientMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	slog.Debug("Action received", "action", msg.Action, "payload", msg)

	switch msg.Action {
	case "init":
		s.initElevator(msg.Config)
	case "addCall":
		if s.elevator != nil {
			if err := s.elevator.AddCall(msg.Floor, true); err != nil {
				// Error is already logged in AddCall, but warning here for WS context is okay
				slog.Warn("Failed to add call via WS", "floor", msg.Floor, "error", err)
			}
			s.sendState()
		}
	case "removeCall":
		if s.elevator != nil {
			s.elevator.RemoveCall(msg.Floor)
			s.sendState()
		}
	case "pressOpen":
		if s.elevator != nil {
			s.elevator.PressOpenButton()
		}
	case "releaseOpen":
		if s.elevator != nil {
			s.elevator.ReleaseOpenButton()
		}
	case "pressClose":
		if s.elevator != nil {
			s.elevator.PressCloseButton()
		}
	case "setMode":
		if s.elevator != nil {
			s.elevator.SetMode(elevator.OperationMode(msg.Mode))
			s.sendState()
		}
	case "reset":
		if s.elevator != nil {
			s.elevator.Reset()
			s.sendState()
		}
	case "stop":
		if s.cancel != nil {
			s.cancel()
		}
		s.elevator = nil
	case "getState":
		if s.elevator != nil {
			s.sendState()
		}
	case "addWeight":
		if s.elevator != nil {
			s.elevator.AddWeight(msg.Weight)
			s.sendState()
		}
	case "setWeight":
		if s.elevator != nil {
			current := s.elevator.Weight()
			delta := msg.Weight - current
			s.elevator.AddWeight(delta)
			s.sendState()
		}
	}
}

func (s *ElevatorSession) initElevator(cfg *ElevatorConfig) {
	if cfg == nil {
		slog.Warn("No config provided for init")
		return
	}

	// Stop existing elevator if any
	if s.cancel != nil {
		s.cancel()
	}

	// Create new elevator with config
	config := elevator.Config{
		ID:             cfg.ID,
		MinFloor:       cfg.MinFloor,
		MaxFloor:       cfg.MaxFloor,
		InitialFloor:   cfg.InitialFloor,
		TravelTime:     time.Duration(cfg.TravelTime * float64(time.Second)),
		TravelTimeEdge: time.Duration(cfg.TravelTime * 1.5 * float64(time.Second)),
		DoorSpeed:      time.Duration(cfg.DoorSpeed * float64(time.Second)),
		DoorOpenTime:   time.Duration(cfg.DoorOpenTime * float64(time.Second)),
		DoorReopenTime: time.Duration(cfg.DoorReopenTime * float64(time.Second)),
		MaxWeight:      1000,
	}
	slog.Info("Elevator config", "config", config)

	e, err := elevator.New(config)
	if err != nil {
		slog.Error("Failed to initialize elevator", "error", err)
		return
	}
	s.elevator = e

	// Subscribe to events
	// 이벤트 구독
	go s.eventListener()

	// Start elevator
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go func() {
		if err := s.elevator.Run(ctx); err != nil && err != context.Canceled {
			slog.Error("Elevator run error", "error", err)
		}
	}()

	slog.Info("Elevator initialized", "id", cfg.ID, "floors", cfg.MinFloor, "to", cfg.MaxFloor)

	// Send initial state
	s.sendState()
}

func (s *ElevatorSession) eventListener() {
	eventCh := s.elevator.Events()
	for {
		select {
		case <-s.done:
			return
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			s.sendEvent(event)
			s.sendState()
		}
	}
}

func (s *ElevatorSession) sendState() {
	if s.elevator == nil {
		return
	}

	floor, direction, doors, weight := s.elevator.CurrentState()
	callFloors := s.elevator.CallFloors()

	doorStates := DoorStates{
		Front: string(doors[elevator.Front]),
		Rear:  string(doors[elevator.Rear]),
	}

	msg := ServerMessage{
		Type:       "state",
		Floor:      floor,
		Direction:  string(direction),
		Doors:      doorStates,
		Mode:       int(s.elevator.Mode),
		CallFloors: callFloors,
		Weight:     weight,
		MaxWeight:  s.elevator.Config.MaxWeight,
	}

	s.writeJSON(msg)
}

func (s *ElevatorSession) sendEvent(event elevator.Event) {
	msg := ServerMessage{
		Type:      "event",
		EventType: string(event.Type),
		Payload:   event.Payload,
		Timestamp: event.Timestamp.Format("15:04:05"),
	}

	s.writeJSON(msg)
}

func (s *ElevatorSession) writeJSON(msg ServerMessage) {
	// slog.Debug("Sending message", "type", msg.Type, "event", msg.EventType) // Optional trace
	if err := s.conn.WriteJSON(msg); err != nil {
		slog.Error("Failed to write JSON message", "error", err)
	}
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WebSocket upgrade failed", "error", err)
		return
	}

	session := NewElevatorSession(conn)
	session.HandleMessages()
}

type AppConfig struct {
	Port string
}

func loadConfig() *AppConfig {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return &AppConfig{
		Port: port,
	}
}

func main() {
	cfg := loadConfig()

	// Serve static files from embedded filesystem
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}

	http.Handle("/", http.FileServer(http.FS(staticFS)))
	http.HandleFunc("/ws", handleWebSocket)

	addr := ":" + cfg.Port
	slog.Info("Starting elevator web server", "addr", addr)
	slog.Info("Open http://localhost:" + cfg.Port + " in your browser")

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
