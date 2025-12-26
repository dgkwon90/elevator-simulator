# Elevator Simulator

Go 언어로 구현된 웹 기반 엘리베이터 시뮬레이터입니다. `pkg/elevator` 패키지의 핵심 로직을 웹 인터페이스를 통해 시각화하고 제어할 수 있습니다.

## 📂 주요 구조

- **`pkg/elevator/`**: 엘리베이터 코어 로직
  - SCAN 스케줄링 알고리즘
  - 상태 머신 (문 열림/닫힘, 이동, 대기 등)
  - 이벤트 기반 동작 (채널 사용)
- **`cmd/web-elevator/`**: 웹 애플리케이션 엔트리포인트
  - WebSocket을 통한 실시간 양방향 통신
  - 임베디드 정적 파일(HTML/CSS/JS) 서빙

## 🚀 실행 방법

```bash
# 의존성 설치
go mod download

# 서버 실행
go run cmd/web-elevator/main.go
```

실행 후 브라우저에서 [http://localhost:8080](http://localhost:8080)으로 접속하여 시뮬레이터를 사용할 수 있습니다.
