# English_zoa

매일 10문제씩 **비즈니스 영어** 퀴즈를 풀고 점수로 경쟁하는 팀 학습 앱. Applied 내부
**Apps Platform v2**(Cloud Run · IAP/Okta) 위에서 동작하며, 배포 패턴은
[MADANG](https://github.com/seungheon-lee-ai/madang) 대시보드와 동일하다
(Go 단일 서비스가 `web/`를 `//go:embed`로 서빙 + `/api/*`). UI는 듀오링고 스타일
(두꺼운 버튼, 스트릭 불꽃, 진한 그린/블루 컬러).

라이브: https://english-zoa.experimental.apps.applied.dev

## 기능

- **퀴즈** — 매일 유저별로 새 10문제가 생성됨 (Seoul 자정 기준). 두 가지 유형이
  섞여 나온다:
  - **객관식** — 영어 문구 보고 한국어 뜻 4개 중 고르기
  - **이어맞추기** — 한국어 뜻 보고 섞인 영어 단어를 순서대로 눌러 문장 완성
  하루 10문제 중 맞힌 문제만 점수(+2점)로 집계되고, 한 문제는 한 번만 채점됨
  (다시 풀어도 점수 안 늘어남) — 자연히 하루 최대 20점(10문제 × 2점)으로 캡됨.
- **개인 페이지** — 닉네임·상태 메시지 편집, 최근 접속 기록(날짜별 첫 접속 시각) 캘린더.
- **리더보드** — 퀴즈 정답 +2점, 7일 연속 로그인마다 +10점 보너스로 팀원끼리 경쟁.

문구 자체는 `#learning-english-with-ai` 슬랙 채널에서 매일 하나씩 자동으로 쌓이고
(연동 전엔 내장 fallback 문구), 퀴즈는 이 풀에서 매일 10개를 뽑아 만든다. "오늘의
문구" 플래시카드 화면은 제거됨 — 문구 소스 파이프라인만 내부적으로 남아있다.

## 섹션별 파일 구조 — 승헌/태훈 병렬 작업용

각 기능이 백엔드 1개 파일 + 프론트 1개 파일로 묶여 있어서, 서로 다른 섹션이면
겹치는 파일 없이 동시에 작업할 수 있다.

| 섹션 | 백엔드 | 프론트 | DB 테이블 |
|---|---|---|---|
| 퀴즈 (하루 10문제) | `quiz.go` | `web/quiz.jsx` | `quiz_questions` (읽기: `phrases`) |
| 문구 소스 (화면 없음) | `phrase.go` (+ `slack.go`) | — | `phrases` |
| 개인 페이지 | `profile.go` | `web/profile.jsx` | `users`, `login_events` |
| 리더보드/점수 | `score.go` | `web/leaderboard.jsx` | `user_scores` |
| 공통 | `main.go`(부트스트랩), `db.go`(연결) | `web/app.jsx`(셸/라우팅), `web/topbar.jsx`, `web/api.js` | — |

각 섹션 파일이 자기 테이블의 `CREATE TABLE` 문(`*SchemaStmts`)과 `register*Routes(r)`를
갖고 있다 — `db.go`의 `initSchema`와 `main.go`의 `main()`이 그것들을 그냥 호출만 한다.
새 섹션을 추가할 땐 이 패턴을 따라 새 `.go`/`.jsx` 파일 하나씩 추가하면 된다.

## 퀴즈 채점 설계 메모

- 정답은 채점 전까지 클라이언트에 절대 노출 안 됨 — 객관식은 옵션에 실제
  phrase id를 그대로 쓰지만(그 자체로는 안 새는 정보) 어느 게 정답인지는 채점
  API 응답에서만 알려주고, 이어맞추기는 단어 칩 id를 랜덤 토큰으로 발급해서
  "id 순서대로 정렬하면 정답"같은 지름길이 없게 함 (`quiz.go` 상단 주석 참고).
- 하루 10문제는 유저별로 한 번 생성되면 고정 — 새로고침해도 안 섞임. 정답/오답도
  한 번 채점되면 DB에 저장되고 재제출은 점수에 영향 없음 (`recordQuizAnswer`).

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
비즈니스 영어 문구 목록(`phrase.go` `fallbackPhrases`)에서 날짜별로 하나씩 풀에
쌓인다. 객관식은 문구가 4개 이상 쌓여야 나오고(1개 정답 + 3개 오답 보기 필요),
그 전까진 이어맞추기만 출제된다.

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
go test ./...   # parsePhraseText, 이어맞추기 칩 재조합, 닉네임/fallback 문구 유닛 테스트
go run .        # 로컬 실행 후 브라우저에서 퀴즈(객관식+이어맞추기) → 완료 화면 → 캘린더 → 리더보드 확인
```
