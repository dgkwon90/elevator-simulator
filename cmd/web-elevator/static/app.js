/**
 * 엘리베이터 시뮬레이터 - WebSocket Client
 * Go 서버의 pkg/elevator 패키지와 통신
 */

// ========================================
// Constants
// ========================================
const Direction = {
    UP: 'Up',
    DOWN: 'Down',
    NONE: 'None'
};

const DoorState = {
    OPEN: 'Open',
    OPENING: 'Opening',
    CLOSING: 'Closing',
    CLOSE: 'Close'
};

const OperationMode = {
    AUTO: 0,
    MANUAL: 1,
    MOVING: 2,
    EMERGENCY: 3
};

const ModeNames = ['Auto', 'Manual', 'Moving', 'Emergency'];

// ========================================
// WebSocket Client
// ========================================
class ElevatorClient {
    constructor() {
        this.ws = null;
        this.eventListeners = [];
        this.stateListeners = [];
        this.state = {
            floor: 1,
            direction: Direction.NONE,
            doors: { front: DoorState.CLOSE, rear: DoorState.CLOSE },
            mode: OperationMode.AUTO,
            callFloors: [],
            weight: 0,
            maxWeight: 1000
        };
    }

    connect() {
        return new Promise((resolve, reject) => {
            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            const wsUrl = `${protocol}//${window.location.host}/ws`;

            this.ws = new WebSocket(wsUrl);

            this.ws.onopen = () => {
                console.log('WebSocket connected');
                resolve();
            };

            this.ws.onerror = (error) => {
                console.error('WebSocket error:', error);
                reject(error);
            };

            this.ws.onclose = () => {
                console.log('WebSocket closed');
            };

            this.ws.onmessage = (event) => {
                const msg = JSON.parse(event.data);
                this.handleMessage(msg);
            };
        });
    }

    handleMessage(msg) {
        if (msg.type === 'state') {
            this.state = {
                floor: msg.floor,
                direction: msg.direction,
                doors: msg.doors,
                mode: msg.mode,
                callFloors: msg.callFloors || [],
                weight: msg.weight || 0,
                maxWeight: msg.maxWeight || 0
            };
            this.stateListeners.forEach(cb => cb(this.state));
        } else if (msg.type === 'event') {
            this.eventListeners.forEach(cb => cb({
                type: msg.eventType,
                payload: msg.payload,
                timestamp: msg.timestamp
            }));
        }
    }

    onState(callback) {
        this.stateListeners.push(callback);
    }

    onEvent(callback) {
        this.eventListeners.push(callback);
    }

    send(action, data = {}) {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.ws.send(JSON.stringify({ action, ...data }));
        }
    }

    init(config) {
        this.send('init', { config });
    }

    addCall(floor) {
        this.send('addCall', { floor });
    }

    removeCall(floor) {
        this.send('removeCall', { floor });
    }

    pressOpen() {
        this.send('pressOpen');
    }

    releaseOpen() {
        this.send('releaseOpen');
    }

    pressClose() {
        this.send('pressClose');
    }

    setWeight(weight) {
        this.send('setWeight', { weight });
    }

    setMode(mode) {
        this.send('setMode', { mode });
    }

    reset() {
        this.send('reset');
    }

    stop() {
        this.send('stop');
    }

    getState() {
        return this.state;
    }

    getFloor() { return this.state.floor; }
    getDirection() { return this.state.direction; }
    getDoors() { return this.state.doors; }
    getMode() { return this.state.mode; }
    getCallFloors() { return this.state.callFloors; }
}

// ========================================
// UI Controller
// ========================================
class UIController {
    constructor() {
        this.client = null;
        this.config = null;
        this.bindElements();
        this.bindEvents();
    }

    bindElements() {
        // Screens
        this.configScreen = document.getElementById('config-screen');
        this.simulationScreen = document.getElementById('simulation-screen');

        // Config form
        this.configForm = document.getElementById('config-form');
        this.minFloorInput = document.getElementById('minFloor');
        this.maxFloorInput = document.getElementById('maxFloor');
        this.initialFloorInput = document.getElementById('initialFloor');
        this.travelTimeInput = document.getElementById('travelTime');
        this.doorSpeedInput = document.getElementById('doorSpeed');
        this.doorOpenTimeInput = document.getElementById('doorOpenTime');
        this.doorReopenTimeInput = document.getElementById('doorReopenTime');

        // Building
        this.building = document.getElementById('building');
        this.elevatorCar = document.getElementById('elevator-car');

        // Status
        this.statusMode = document.getElementById('status-mode');
        this.statusDirection = document.getElementById('status-direction');
        this.statusFloor = document.getElementById('status-floor');
        this.statusDoor = document.getElementById('status-door');

        // Controls
        this.floorButtons = document.getElementById('floor-buttons');
        this.btnOpen = document.getElementById('btn-open');
        this.btnClose = document.getElementById('btn-close');
        this.modeSelect = document.getElementById('mode-select');
        this.btnReset = document.getElementById('btn-reset');
        this.btnStop = document.getElementById('btn-stop');

        // Weight
        this.weightSlider = document.getElementById('weight-slider');
        this.weightValue = document.getElementById('weight-value');
        this.weightMax = document.getElementById('weight-max');
        this.overloadIndicator = document.getElementById('overload-indicator');

        // Log
        this.eventLog = document.getElementById('event-log');
        this.btnClearLog = document.getElementById('btn-clear-log');

        // Back
        this.btnBack = document.getElementById('btn-back');
    }

    bindEvents() {
        // Config form submit
        this.configForm.addEventListener('submit', (e) => {
            e.preventDefault();
            this.startSimulation();
        });

        // Door buttons
        this.btnOpen.addEventListener('mousedown', () => {
            if (this.client) this.client.pressOpen();
        });
        this.btnOpen.addEventListener('mouseup', () => {
            if (this.client) this.client.releaseOpen();
        });
        this.btnOpen.addEventListener('mouseleave', () => {
            if (this.client) this.client.releaseOpen();
        });
        this.btnClose.addEventListener('click', () => {
            if (this.client) this.client.pressClose();
        });

        // Mode select
        this.modeSelect.addEventListener('change', () => {
            if (this.client) {
                this.client.setMode(parseInt(this.modeSelect.value));
            }
        });

        // Reset
        this.btnReset.addEventListener('click', () => {
            if (this.client) {
                this.client.reset();
                this.addLog('🔄 시뮬레이터가 리셋되었습니다.', 'info');
            }
        });

        // Stop
        this.btnStop.addEventListener('click', () => {
            if (this.client) {
                this.client.setMode(OperationMode.EMERGENCY);
                this.modeSelect.value = OperationMode.EMERGENCY;
            }
        });

        // Weight slider
        this.weightSlider.addEventListener('input', () => {
            this.weightValue.textContent = this.weightSlider.value;
        });
        this.weightSlider.addEventListener('change', () => {
            if (this.client) {
                this.client.setWeight(parseInt(this.weightSlider.value));
            }
        });

        // Clear log
        this.btnClearLog.addEventListener('click', () => {
            this.eventLog.innerHTML = '';
        });

        // Back button
        this.btnBack.addEventListener('click', () => {
            this.stopSimulation();
        });
    }

    async startSimulation() {
        this.config = {
            id: 'WEB-ELV',
            minFloor: parseInt(this.minFloorInput.value),
            maxFloor: parseInt(this.maxFloorInput.value),
            initialFloor: parseInt(this.initialFloorInput.value),
            travelTime: parseFloat(this.travelTimeInput.value),
            doorSpeed: parseFloat(this.doorSpeedInput.value),
            doorOpenTime: parseFloat(this.doorOpenTimeInput.value),
            doorReopenTime: parseFloat(this.doorReopenTimeInput.value),
        };

        // Validate
        if (this.config.initialFloor < this.config.minFloor || this.config.initialFloor > this.config.maxFloor) {
            alert('시작 층이 유효하지 않습니다.');
            return;
        }

        // Create WebSocket client
        this.client = new ElevatorClient();

        try {
            await this.client.connect();
        } catch (error) {
            alert('서버 연결에 실패했습니다. Go 서버가 실행 중인지 확인하세요.');
            return;
        }

        // Subscribe to state updates
        this.client.onState((state) => this.updateUI(state));

        // Subscribe to events
        this.client.onEvent((event) => this.handleEvent(event));

        // Build UI
        this.buildFloorUI(this.config);
        this.buildFloorButtons(this.config);

        // Initialize elevator on server
        this.client.init(this.config);

        // Switch screens
        this.configScreen.classList.add('hidden');
        this.simulationScreen.classList.remove('hidden');

        // Clear log and add start message
        this.eventLog.innerHTML = '';
        this.addLog('🚀 시뮬레이터가 시작되었습니다. (Go 서버 연결됨)', 'info');

        // Wait for DOM to render, then update position
        requestAnimationFrame(() => {
            requestAnimationFrame(() => {
                this.updateElevatorPosition(this.config.initialFloor);
            });
        });
    }

    stopSimulation() {
        if (this.client) {
            this.client.stop();
            this.client = null;
        }

        this.simulationScreen.classList.add('hidden');
        this.configScreen.classList.remove('hidden');
    }

    buildFloorUI(config) {
        this.building.innerHTML = '';
        this.floorElements = {};

        for (let f = config.maxFloor; f >= config.minFloor; f--) {
            const floorDiv = document.createElement('div');
            floorDiv.className = 'floor';
            floorDiv.dataset.floor = f;

            const label = document.createElement('span');
            label.className = 'floor-label';
            label.textContent = this.formatFloorName(f);

            const indicator = document.createElement('div');
            indicator.className = 'floor-indicator';

            floorDiv.appendChild(label);
            floorDiv.appendChild(indicator);

            this.building.appendChild(floorDiv);
            this.floorElements[f] = floorDiv;
        }
    }

    buildFloorButtons(config) {
        this.floorButtons.innerHTML = '';
        this.floorButtonElements = {};

        // Create buttons from max to min
        for (let f = config.maxFloor; f >= config.minFloor; f--) {
            const btn = document.createElement('button');
            btn.className = 'btn-floor';
            btn.textContent = this.formatFloorName(f);
            btn.dataset.floor = f;

            btn.addEventListener('click', () => {
                if (this.client) {
                    // Toggle: if already called, remove; otherwise add
                    const callFloors = this.client.getCallFloors();
                    if (callFloors.includes(f)) {
                        this.client.removeCall(f);
                    } else {
                        this.client.addCall(f);
                    }
                }
            });

            this.floorButtons.appendChild(btn);
            this.floorButtonElements[f] = btn;
        }
    }

    formatFloorName(floor) {
        if (floor === 0) return 'G';
        if (floor < 0) return `B${Math.abs(floor)}`;
        return `${floor}F`;
    }

    handleEvent(event) {
        const eventType = event.type;
        const payload = event.payload;

        switch (eventType) {
            case 'FloorChange':
                // Go sends just the floor number as payload for FloorChange
                const floorValue = typeof payload === 'number' ? payload : (payload?.to || payload);
                this.addLog(`📍 층 변경: ${this.formatFloorName(floorValue)}`, 'floor');
                break;
            case 'DoorChange':
                // Go sends { Side: number, State: string }
                const doorState = payload?.State || payload?.state || payload;
                const doorIcon = doorState === 'Open' ? '🚪↔️' :
                    doorState === 'Close' ? '🚪' :
                        doorState === 'Opening' ? '🚪→' : '🚪←';
                this.addLog(`${doorIcon} 문 상태: ${doorState}`, 'door');
                break;
            case 'DirectionChange':
                // Go sends the direction string as payload
                const direction = typeof payload === 'string' ? payload : (payload?.to || payload);
                const dirIcon = direction === 'Up' ? '⬆️' :
                    direction === 'Down' ? '⬇️' : '⏹';
                this.addLog(`${dirIcon} 방향 변경: ${direction}`, 'direction');
                break;
            case 'ModeChange':
                // Go sends OperationMode (int) as payload
                const modeValue = typeof payload === 'number' ? payload : (payload?.to || 0);
                this.addLog(`⚙️ 모드 변경: ${ModeNames[modeValue] || modeValue}`, 'mode');
                break;
            default:
                this.addLog(`📌 ${eventType}: ${JSON.stringify(payload)}`, 'info');
        }
    }

    updateUI(state) {
        if (!state) return;
        console.log('Updating UI with state:', state); // Debug log

        // Status
        const mode = state.mode;
        this.statusMode.textContent = ModeNames[mode];
        this.statusMode.className = `status-value mode-${ModeNames[mode].toLowerCase()}`;

        const dir = state.direction;
        const dirIcon = dir === Direction.UP ? '⬆️' : dir === Direction.DOWN ? '⬇️' : '⏹';
        this.statusDirection.innerHTML = `<span class="direction-icon">${dirIcon}</span> ${dir}`;

        const floor = state.floor;
        this.statusFloor.textContent = this.formatFloorName(floor);

        const doorState = state.doors.front;
        const doorIcon = doorState === DoorState.OPEN ? '🚪↔️' : '🚪';
        this.statusDoor.innerHTML = `<span class="door-icon">${doorIcon}</span> ${doorState}`;

        // Elevator position
        this.updateElevatorPosition(floor);

        // Elevator door animation
        this.updateElevatorDoors(doorState);

        // Floor indicators
        this.updateFloorIndicators(state);

        // Floor buttons
        this.updateFloorButtons(state);

        // Weight
        this.updateWeight(state);
    }

    updateWeight(state) {
        const weight = state.weight;
        const maxWeight = state.maxWeight;

        // Only update slider if not being dragged (check active element)
        if (document.activeElement !== this.weightSlider) {
            this.weightSlider.value = weight;
        }

        this.weightValue.textContent = weight;
        this.weightMax.textContent = `/ ${maxWeight}kg`;

        // Update slider max if needed (ensure it covers user input range or config)
        if (parseInt(this.weightSlider.max) < maxWeight * 1.5) {
            this.weightSlider.max = maxWeight * 1.5;
        }

        if (weight > maxWeight) {
            this.overloadIndicator.classList.remove('hidden');
            this.weightValue.style.color = '#ff4d4d';
        } else {
            this.overloadIndicator.classList.add('hidden');
            this.weightValue.style.color = '';
        }
    }

    updateElevatorPosition(floor) {
        if (!this.floorElements[floor]) return;

        const floorEl = this.floorElements[floor];
        const buildingRect = this.building.getBoundingClientRect();
        const floorRect = floorEl.getBoundingClientRect();

        const top = floorRect.top - buildingRect.top + 2;
        this.elevatorCar.style.top = `${top}px`;
    }

    updateElevatorDoors(state) {
        this.elevatorCar.classList.remove('door-open', 'door-opening', 'door-closing');

        if (state === DoorState.OPEN) {
            this.elevatorCar.classList.add('door-open');
        } else if (state === DoorState.OPENING) {
            this.elevatorCar.classList.add('door-opening');
        } else if (state === DoorState.CLOSING) {
            this.elevatorCar.classList.add('door-closing');
        }
    }

    updateFloorIndicators(state) {
        const callFloors = new Set(state.callFloors || []);
        const currentFloor = state.floor;

        for (const [floor, el] of Object.entries(this.floorElements)) {
            el.classList.remove('active', 'called');

            if (parseInt(floor) === currentFloor) {
                el.classList.add('active');
            }
            if (callFloors.has(parseInt(floor))) {
                el.classList.add('called');
            }
        }
    }

    updateFloorButtons(state) {
        const callFloors = new Set(state.callFloors || []);
        const currentFloor = state.floor;

        for (const [floor, btn] of Object.entries(this.floorButtonElements)) {
            btn.classList.remove('called', 'current');

            if (callFloors.has(parseInt(floor))) {
                btn.classList.add('called');
            }
            if (parseInt(floor) === currentFloor) {
                btn.classList.add('current');
            }
        }
    }

    addLog(message, type = 'info') {
        const entry = document.createElement('div');
        entry.className = `log-entry log-${type}`;

        const time = new Date().toLocaleTimeString('ko-KR', {
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit'
        });

        entry.innerHTML = `
            <span class="log-time">${time}</span>
            <span class="log-message">${message}</span>
        `;

        // Prepend to show newest first
        this.eventLog.insertBefore(entry, this.eventLog.firstChild);

        // Limit log entries
        while (this.eventLog.children.length > 100) {
            this.eventLog.removeChild(this.eventLog.lastChild);
        }
    }
}

// ========================================
// Initialize
// ========================================
document.addEventListener('DOMContentLoaded', () => {
    new UIController();
});
