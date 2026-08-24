# PhraseUp

매일 10문제씩 **비즈니스 영어** 퀴즈를 풀고 경쟁하는 팀 학습 앱. Applied 내부
**Apps Platform v2**(Cloud Run · IAP/Okta) 위에서 동작하며, 배포 패턴은
[MADANG](https://github.com/seungheon-lee-ai/madang) 대시보드와 동일하다
(Go 단일 서비스가 `web/`를 `//go:embed`로 서빙 + `/api/*`). UI는 전부 영어,
듀오링고 스타일(두꺼운 버튼, 스트릭 불꽃, 진한 그린/블루 컬러)로 되어 있다.

라이브: https://phraseup.experimental.apps.applied.dev

앱 이름(브랜드/서비스명/GitHub 리포)을 `English_zoa`/`english-zoa`에서
`PhraseUp`/`phraseup`으로 전면 변경함 — Cloud Run 서비스·서비스 계정·DB 스키마
모두 새 이름으로 새로 등록. **예전 `english-zoa` 서비스의 데이터(유저/퀴즈 기록)는
새 서비스 계정에 자동으로 넘어오지 않아 리셋됨** (사용자가 리스크를 인지하고 승인).

## 화면 구성

- **Main** — 홈. 가운데 TED Talk 영상(하루 하나, 좌우로 지난 영상 넘겨보기),
  우측에 팀 프레즌스(누가 지금 접속 중인지 + 상태 메시지) + 미니 리더보드.
- **Quiz** — 매일 10문제.
- **Profile** — 네브 탭이 아니라 우측 상단 아바타 버튼으로만 진입. 닉네임/상태
  메시지 편집 + 스트릭 히어로 + 이번 달 캘린더(요일 헤더 있는 실제 월간 그리드,
  로그인한 날 🔥 표시).
- 리더보드는 별도 화면이 아니라 Main 페이지 사이드바에 작게 표시됨.

## 기능

- **퀴즈** — 매일 유저별로 새 10문제가 생성됨 (Seoul 자정 기준). 두 가지 유형이
  섞여 나온다:
  - **Multiple Choice** — 영어 문구/단어 보고 한국어 뜻 4개 중 고르기
  - **Word Order** — 한국어 뜻 보고 섞인 영어 단어를 순서대로 눌러 문장 완성
    (단어 1개짜리 vocabulary 항목은 대상에서 제외)
  콘텐츠는 **Vocabulary**(단어/콜로케이션)와 **Expression**(전체 문장) 두 카테고리가
  섞여 나온다. 한 문제는 한 번만 채점되고, **오답이어도 아무것도 깎이지 않는다** —
  "점수"는 그냥 맞힌 문제 개수의 누적 COUNT라서 뺄 게 없음.
- **TED Talk of the Day** (`tedtalk.go`, `web/main.jsx`) — 검증된 실제 TED 영상
  8개를 날짜 기준으로 결정론적으로 로테이션 (DB 테이블 없음, 고정 앵커 날짜로부터
  일수 계산). YouTube iframe으로 바로 재생, 최근 7일 날짜칩으로 과거 영상도 탐색.
- **팀 프레즌스** (`profile.go` `/api/team`) — 마지막 API 호출 후 5분 이내면 온라인
  표시. `main.go`의 `requireEmail`이 인증된 요청마다 `last_active_at`을 비동기로 갱신.
- **리더보드 (2종, 서로 완전히 분리)**:
  1. **Most Correct Answers** — 전체 기간 누적 정답 개수 순위
  2. **Longest Streak This Month** — 이번 달 최고 연속 로그인 순위
  퀴즈 점수와 로그인 스트릭은 서로 다른 지표로 완전히 독립적이다.

## 문구 풀 — AI + 정적 콘텐츠 + Slack

문구 풀은 앱이 뜨자마자 `phrase.go`의 `fallbackPhrases`(현재 50개, vocabulary/
expression 섞임)를 **한 번에 전부** 시딩한다 — "하루에 1개씩"만 쌓는 예전 구조는
배포 이틀 만에 "문제가 2개밖에 없다"는 버그로 이어졌었음. 지금은:

1. **정적 시딩** (`seedStaticPhrasesIfMissing`, `phrase.go`) — 즉시, API 키 불필요
2. **AI 생성** (`ai.go`) — 풀이 15개 밑으로 떨어지면 Anthropic Claude API로 vocabulary
   + expression 20개씩 배치 생성해서 보충. `ANTHROPIC_API_KEY` 없으면 조용히 스킵
3. **Slack** (`slack.go`) — `#learning-english-with-ai`에서 매일 문구 1개씩 추가
   (채널 접근 미확정 — 아래 참고)

퀴즈는 이 풀에서 매일 10개를 뽑아 만든다. `ensureDailyQuiz`는 "오늘 세트가 조금이라도
있으면 그대로 반환"이 아니라 **`seq` 슬롯 단위로 부족분만 채운다** — 예전 리비전에서
남은 부분 세트가 있어도 나머지를 채워서 항상 10개를 채워준다 (기존 정답 기록은
보존됨).

### AI 연동 설정

```bash
# 로컬
export ANTHROPIC_API_KEY=sk-ant-...

# 프로덕션 — apps-platform 전용 명령 사용 (플랫폼팀 권한 없이도 됨, gcloud secrets
# create는 secretmanager.secrets.create 권한이 없어서 실패할 수 있음)
apps-platform app secret set ANTHROPIC_API_KEY "$(pbpaste)"
# → 자동으로 "phraseup-anthropic-api-key" 시크릿 이름이 됨 (서비스명 접두사 자동)
```

`project.toml`의 `enable_secrets = true`가 앱 서비스 계정에 Secret Manager 접근
권한을 준다. 키가 없어도 정적 콘텐츠만으로 앱은 완전히 동작한다.

## 섹션별 파일 구조 — 승헌/태훈 병렬 작업용

| 섹션 | 백엔드 | 프론트 | DB 테이블 |
|---|---|---|---|
| 퀴즈 (하루 10문제) | `quiz.go` | `web/quiz.jsx` | `quiz_questions` (읽기: `phrases`) |
| 문구 소스 (화면 없음) | `phrase.go`, `slack.go`, `ai.go` | — | `phrases` |
| 메인 (TED Talk + 팀 + 미니 리더보드) | `tedtalk.go` | `web/main.jsx` | (테이블 없음) |
| 개인 페이지 | `profile.go` | `web/profile.jsx` | `users`(+`last_active_at`), `login_events` |
| 리더보드 (집계만, 화면은 main.jsx) | `score.go` | — | (테이블 없음 — `quiz_questions`/`login_events` 집계) |
| 공통 | `main.go`(부트스트랩), `db.go`(연결) | `web/app.jsx`(셸/라우팅), `web/topbar.jsx`, `web/api.js` | — |

각 섹션 파일이 자기 테이블의 `CREATE TABLE` 문(`*SchemaStmts`)과 `register*Routes(r)`를
갖고 있다 — `db.go`의 `initSchema`와 `main.go`의 `main()`이 그것들을 그냥 호출만 한다.

## 퀴즈 채점 설계 메모

- 정답은 채점 전까지 클라이언트에 절대 노출 안 됨 — 이어맞추기는 단어 칩 id를
  랜덤 토큰으로 발급해서 "id 순서대로 정렬하면 정답"같은 지름길이 없게 함
  (`quiz.go` 상단 주석 참고).
- 하루 10문제는 유저별로 생성되면 고정 — 새로고침해도 안 섞임. `seq` 슬롯이
  비어있으면 그 슬롯만 채움(부분 세트 버그 재발 방지, 위 참고).
- "점수"는 `quiz_questions.result = 'correct'`의 COUNT — 오답을 아무리 내도
  깎일 게 없음.

## 로컬 실행

```bash
export DB_USER=<로컬 postgres 유저>
export DB_NAME=postgres
export DEV_USER_EMAIL=you@applied.co

go run .   # http://localhost:8080
```

AI/Slack 연동 없이도 바로 동작 — 앱이 뜨자마자 정적 fallback 목록(50개)이 풀에
시딩된다.

## Slack 연동 — 확인 필요

`#learning-english-with-ai` 채널의 실제 메시지 포맷을 아직 확인하지 못했다.
`slack.go`의 `parsePhraseText`는 다음 포맷들을 우선 지원한다:

- `EN: ...` / `KR: ...` 라벨링된 두 줄 (순서 무관)
- 평범한 두 줄 (첫 줄 = 영어, 둘째 줄 = 한국어)
- 한 줄 `English phrase (한국어 번역)`

필요한 값: `SLACK_CHANNEL_ID`, `SLACK_SEED_USER_EMAIL` (기본값 태훈).

## 배포 (Apps Platform v2)

```bash
apps-platform app deploy   # go.mod 있으면 자동 --no-build 모드, Docker 불필요
```

`project.toml` name = `phraseup`, owner = 태훈, maintainer = 승헌. 새로 배포 권한이
필요하면 `#eng-apps-platform-v2`에 앱 이름 + 이메일을 보내면 된다.

## 검증

```bash
go test ./...
go run .   # 브라우저에서 Main(TED Talk+팀+미니 리더보드) → Quiz → 아바타로 Profile 확인
```
