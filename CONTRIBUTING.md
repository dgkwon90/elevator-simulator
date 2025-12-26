# Golang 개발 가이드라인 및 기여 규칙

> **"Clear is better than clever."**  
> (명확함이 똑똑함보다 낫습니다.)

이 문서는 우리 프로젝트의 **코드 품질, 가독성, 유지보수성**을 보장하기 위한 Golang 개발의 **Single Source of Truth (SSOT)**입니다.  
모든 기여자는 이 가이드를 숙지하고 준수해야 하며, 코드 리뷰의 기준이 됩니다.

---

## 1. 프로젝트 아키텍처 및 구조 (Project Architecture)

우리는 **Standard Go Project Layout**과 **Clean Architecture** 원칙을 따릅니다.

### 1.1 디렉토리 구조

- **`cmd/`**: 애플리케이션의 엔트리포인트(main 패키지). 비즈니스 로직을 포함하지 않습니다.
- **`internal/`**: 외부에서 임포트할 수 없는 비공개 애플리케이션 및 라이브러리 코드.
  - `internal/domain`: 핵심 도메인 로직 및 모델.
  - `internal/service`: 비즈니스 로직 (Use Cases).
  - `internal/repository`: 데이터 접근 계층.
  - `internal/handler`: HTTP/gRPC 핸들러.
- **`pkg/`**: 외부 프로젝트에서도 사용할 수 있는 범용 라이브러리 코드. (신중하게 사용)
- **`api/`**: Protobuf, OpenAPI 정의 등 API 스키마.
- **`configs/`**: 설정 파일 템플릿 및 기본값.
- **`test/`**: 추가적인 외부 테스트 애플리케이션 및 테스트 데이터.

### 1.2 아키텍처 원칙

- **의존성 역전 (Dependency Inversion)**: 고수준 모듈(Service)은 저수준 모듈(Repository)에 의존하지 않고, 인터페이스에 의존해야 합니다.
- **계층화 (Layering)**: `Handler` -> `Service` -> `Repository` 순으만 호출합니다. 역방향 호출은 금지합니다.
- **구성(Composition) 우선**: 상속보다는 인터페이스와 구조체 임베딩을 통한 구성을 선호합니다.

---

## 2. 네이밍 규칙 (Naming Conventions)

이름 짓기는 개발의 90%입니다. Go는 **짧고 명확한** 이름을 선호합니다.

### 2.1 패키지명 (Packages)

- **규칙**: 소문자 한 단어 사용. 언더스코어(`_`)나 대문자 섞지 않음.
- **Good**: `package user`, `package http`, `package json`
- **Bad**: `package User`, `package user_service`, `package httpHelpers`

### 2.2 파일명 (Files)

- **규칙**: 소문자와 언더스코어(`snake_case`) 사용.
- **Good**: `user.go`, `user_service.go`, `user_test.go`
- **Bad**: `User.go`, `UserService.go`

### 2.3 변수명 (Variables)

- **Scope에 따른 길이**: 변수의 생명 주기(Scope)가 짧을수록 이름도 짧게, 길수록 설명적으로 짓습니다.
  - **Loop/If**: `i`, `v`, `err` 등 1-2글자 선호.
  - **함수 인자**: `ctx`, `req`, `resp`, `db` 등 널리 쓰이는 약어 사용.
  - **전역/필드**: `RequestTimeout` 같이 충분히 설명적으로 작성.
- **약어 대문자 규칙 (Acronyms)**: `URL`, `ID`, `HTTP`, `JSON` 같은 약어는 전체가 대문자거나 전체가 소문자여야 합니다.
  - **Good**: `ServeHTTP`, `userID`, `xmlHTTPRequest`
  - **Bad**: `ServeHttp`, `userId`

### 2.4 함수명 (Functions)

- **Getter**: `Get` 접두어를 붙이지 않습니다. (`user.Name()` O, `user.GetName()` X)
- **Setter**: `Set` 접두어를 붙일 수 있습니다.
- **동사 선택**:
  - `Find`: 조건 검색 (없으면 nil/ErrNotFound). 예: `FindUserByEmail`
  - `Get`: ID 조회 (없으면 Error). 예: `GetUser`
  - `Fetch`: 원격지(DB, Network) 요청 강조.
  - `List`: 목록 조회.
  - `Is`: Boolean 반환.

### 2.5 인터페이스명 (Interfaces)

- **단일 메서드**: 메서드 이름 + `er`. (예: `Reader`, `Writer`, `Doer`)
- **다중 메서드**: 역할에 맞는 이름. (예: `Repository`, `Service`)

---

## 3. 코드 스타일 및 포맷팅 (Style & Formatting)

### 3.1 포맷팅

- 모든 코드는 `gofmt` (또는 `goimports`)로 포맷팅되어야 합니다.
- IDE 설정에서 저장 시 자동 포맷팅을 활성화하세요.

### 3.2 들여쓰기 최소화 (Keep Left)

- 불필요한 `else`를 제거하고 **Early Return**을 사용하여 코드의 들여쓰기 깊이를 줄입니다.
  
  ```go
  // Good
  if err != nil {
      return err
  }
  return success
  ```

### 3.3 임포트 그룹화 (Imports)

- 표준 라이브러리 | 서드파티 라이브러리 | 내부 패키지 순으로 빈 줄을 두어 구분합니다.

---

## 4. 상세 구현 가이드 (Implementation Guide)

### 4.1 생성자 패턴 (Constructors)

- 구조체 초기화는 `New` 함수를 사용합니다.
- 필수 의존성은 인자로 받고, 선택적 설정은 **Functional Options Pattern**을 고려합니다.

  ```go
  func NewServer(addr string, opts ...Option) *Server
  ```

### 4.2 슬라이스 및 맵 (Slices & Maps)

- 크기를 미리 알 수 있다면 반드시 **`make`로 용량(capacity)을 할당**하여 메모리 재할당을 방지합니다.

  ```go
  users := make([]User, 0, len(ids))
  ```

### 4.3 포인터 사용 기준

- **사용**: 내부 상태를 변경해야 할 때(Modifier), 구조체가 매우 클 때(>64 bytes).
- **비사용**: 단순 조회용(Getter), 작은 구조체, Slice/Map/Channel(이미 참조 타입임).
- **주의**: 과도한 포인터 사용은 GC 부하를 증가시킵니다.

### 4.4 에러 처리 (Error Handling)

- **에러 래핑**: 에러 발생 시 반드시 문맥(Context)을 추가하여 감쌉니다.

  ```go
  return fmt.Errorf("failed to decode user json: %w", err)
  ```

- **Panic 금지**: `main` 함수나 `init`을 제외하고는 절대 `panic`을 사용하지 않습니다. 항상 `error`를 리턴하세요.

### 4.5 동시성 (Concurrency)

- **Context 전파**: 모든 I/O 및 긴 작업 함수는 `context.Context`를 첫 번째 인자로 받아야 합니다.
- **고루틴 생명주기 관리**: 고루틴은 생성한 곳에서 종료를 책임져야 합니다. `sync.WaitGroup` 또는 `errgroup`을 사용하여 고루틴 누수(Leak)를 방지하세요.
- **채널 vs 뮤텍스**:
  - 데이터 소유권 이동/흐름 제어 -> **Channel**
  - 단순 상태 보호(Cache, Counter) -> **Mutex**

---

## 5. 테스트 (Testing)

### 5.1 Table Driven Tests

- Go의 테스트 표준 패턴입니다. 테스트 케이스 데이터와 검증 로직을 분리하세요.

  ```go
  func TestAdd(t *testing.T) {
      tests := []struct {
          name string
          a, b int
          want int
      }{
          {"positive", 1, 2, 3},
          {"negative", -1, -1, -2},
      }
      for _, tt := range tests {
          t.Run(tt.name, func(t *testing.T) {
              if got := Add(tt.a, tt.b); got != tt.want {
                  t.Errorf("Add() = %v, want %v", got, tt.want)
              }
          })
      }
  }
  ```

### 5.2 모의 객체 (Mocking)

- 인터페이스를 적극 활용하여 테스트 시 외부 의존성을 Mocking 합니다.
- 직접 작성하거나 `mockery` 등의 도구를 사용합니다.

---

## 6. 관측 가능성 (Observability)

### 6.1 로깅 (Logging)

- **Structured Logging**: `slog` 패키지를 사용하여 JSON 형태의 구조화된 로그를 남깁니다.
- **Level**:
  - `Info`: 정상 흐름 (로그인 성공, 상태 변경)
  - `Warn`: 예상된 에러/경고 (잘못된 입력)
  - `Error`: 운영자 개입이 필요한 시스템 에러
  - `Debug`: 개발용 상세 정보

### 6.2 추적 (Tracing)

- 오픈텔레메트리(OpenTelemetry) 표준을 따르며, 함수 간 호출 시 `context`를 통해 Trace ID를 전파해야 합니다.

---

## 7. Git 워크플로우 및 체크리스트

### 7.1 커밋 메시지 (Commit Messages)

- **제목**: "타입: 설명" 형식 (예: `feat: add user login handler`, `fix: resolve race condition`)
- **타입**: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`
- 본문은 선택 사항이며, '왜' 변경했는지를 설명합니다.

### 7.2 PR 체크리스트

- [ ] 코드가 스타일 가이드를 준수하는가? (`gofmt`, `goimports`, `golangci-lint`)
- [ ] 테스트 코드가 작성되었고 통과하는가?
- [ ] 불필요한 전역 변수나 복잡한 로직은 없는가?
- [ ] 에러 처리는 꼼꼼하게 되어 있는가?
