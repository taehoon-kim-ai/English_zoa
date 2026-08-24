# English_zoa

매일 **비즈니스 영어** 문구를 풀고 점수로 경쟁하는 팀 학습 앱. Applied 내부
**Apps Platform v2**(Cloud Run · IAP/Okta) 위에서 동작하며, 배포 패턴은
[MADANG](https://github.com/seungheon-lee-ai/madang) 대시보드와 동일하다
(Go 단일 서비스가 `web/`를 `//go:embed`로 서빙 + `/api/*`). UI는 듀오링고 스타일
(두꺼운 버튼, 스트릭 불꽃, 진한 그린/블루 컬러).

라이브: https://english-zoa.experimental.apps.applied.dev

## 기능

- **오늘의 문구** — `#learning-english-with-ai` 슬랙 채널에서 자동으로 가져온 비즈니스
  영어 문구를 플래시카드로 (영어 앞면 → 클릭하면 한국어 뒷면). "알아요"/"몰라요"로 응답.
- **퀴즈** — 지난 문구들로 만든 4지선다. 아직 안 풀어본 문구가 우선 출제되고, 다
  풀면 연습 모드(점수 없음)로 계속 풀 수 있음.
- **개인 페이지** — 닉네임·상태 메시지 편집, 최근 접속 기록(날짜별 첫 접속 시각) 캘린더.
- **리더보드** — 플래시카드 정답 +1점, 퀴즈 정답 +2점, 7일 연속 로그인마다 +10점
  보너스로 팀원끼리 경쟁.

## 섹션별 파일 구조 — 승헌/태훈 병렬 작업용

각 기능이 백엔드 1개 파일 + 프론트 1개 파일로 묶여 있어서, 서로 다른 섹션이면
겹치는 파일 없이 동시에 작업할 수 있다.

| 섹션 | 백엔드 | 프론트 | DB 테이블 |
|---|---|---|---|
| 오늘의 문구 | `phrase.go` (+ `slack.go`) | `web/home.jsx` | `phrases`, `card_attempts` |
| 퀴즈 | `quiz.go` | `web/quiz.jsx` | `quiz_attempts` (읽기: `phrases`) |
| 개인 페이지 | `profile.go` | `web/profile.jsx` | `users`, `login_events` |
| 리더보드/점수 | `score.go` | `web/leaderboard.jsx` | `user_scores` |
| 공통 | `main.go`(부트스트랩), `db.go`(연결) | `web/app.jsx`(셸/라우팅), `web/topbar.jsx`, `web/api.js` | — |

각 섹션 파일이 자기 테이블의 `CREATE TABLE` 문(`*SchemaStmts`)과 `register*Routes(r)`를
갖고 있다 — `db.go`의 `initSchema`와 `main.go`의 `main()`이 그것들을 그냥 호출만 한다.
새 섹션을 추가할 땐 이 패턴을 따라 새 `.go`/`.jsx` 파일 하나씩 추가하면 된다.

## 로컬 실행

```bash
# 1) DB (로컬 Postgres 또는 `apps-platform connect-db` 터널)
export DB_USER=<로컬 postgres 유저>
export DB_NAME=postgres

# 2) IAP 없이 로그인 시뮬레이션
export DEV_USER_EMAIL=you@applied.co

go run .   # http://localhost:8080
```

Slack 연동 없이도 동작한다 — `SLACK_CHANNEL_ID`가 비어 있으면 내장 fallback
비즈니스 영어 문구 목록(`phrase.go` `fallbackPhrases`)에서 날짜별로 하나씩 보여준다.
퀴즈는 문구가 4개 이상 쌓여야 뜬다 (1개 정답 + 3개 오답 보기를 만들 수 있어야 함).

## Slack 연동 — 확인 필요

`#learning-english-with-ai` 채널의 실제 메시지 포맷을 아직 확인하지 못했다
(개발 중 사용한 Slack 커넥터가 이 채널을 보지 못함). `slack.go`의
`parsePhraseText`는 다음 포맷들을 우선 지원하도록 짜여 있다:

- `EN: ...` / `KR: ...` 라벨링된 두 줄 (순서 무관)
- 평범한 두 줄 (첫 줄 = 영어, 둘째 줄 = 한국어)
- 한 줄 `English phrase (한국어 번역)`

실제 채널 메시지가 이 중 어느 것과도 다르면 `parsePhraseText`만 조정하면 된다
(`slack_test.go`에 케이스별 유닛 테스트 있음).

필요한 값:

- `SLACK_CHANNEL_ID` — 채널 ID (Slack UI에서 "채널 ID 복사")
- `SLACK_SEED_USER_EMAIL` — Data API async-token을 발급받을 시드 유저 (기본값 태훈)

## 배포 (Apps Platform v2)

이미 배포되어 있음 (`project.toml` name = `english-zoa`, owner = 태훈,
maintainer = 승헌). 재배포:

```bash
apps-platform app deploy   # go.mod 있으면 자동 --no-build 모드, Docker 불필요
```

새로 배포 권한이 필요하면 `#eng-apps-platform-v2`에 앱 이름 + 이메일을 보내면 된다.
`#learning-english-with-ai` Slack 연동은 별도로 채널 초대 + Data API 스코프
(`channels:history`, `channels:read`) 승인을 플랫폼팀에 요청해야 한다.

## 검증

```bash
go test ./...   # parsePhraseText, 닉네임/fallback 문구 유닛 테스트
go run .        # 로컬 실행 후 브라우저에서 플래시카드 flip → 퀴즈 → 캘린더 → 리더보드 확인
```
