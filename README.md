# English_zoa

매일 영어 문구를 플래시카드로 풀고 점수로 경쟁하는 팀 학습 앱. Applied 내부
**Apps Platform v2**(Cloud Run · IAP/Okta) 위에서 동작하며, 배포 패턴은
[MADANG](https://github.com/seungheon-lee-ai/madang) 대시보드와 동일하다
(Go 단일 서비스가 `web/`를 `//go:embed`로 서빙 + `/api/*`).

## 기능

- **오늘의 문구** — `#learning-english-with-ai` 슬랙 채널에서 자동으로 가져온 문구를
  플래시카드로 (영어 앞면 → 클릭하면 한국어 뒷면). "알아요"/"몰라요"로 응답.
- **개인 페이지** — 닉네임·상태 메시지 편집, 최근 접속 기록(날짜별 첫 접속 시각) 캘린더.
- **리더보드** — 정답 시 +1점, 7일 연속 로그인마다 +10점 보너스로 팀원끼리 경쟁.

## 로컬 실행

```bash
# 1) DB (로컬 Postgres 또는 `apps-platform connect-db` 터널)
export DB_USER=<로컬 postgres 유저>
export DB_NAME=postgres

# 2) IAP 없이 로그인 시뮬레이션
export DEV_USER_EMAIL=you@applied.co

go run .   # http://localhost:8080
```

Slack 연동 없이도 동작한다 — `SLACK_CHANNEL_ID`가 비어 있으면 내장 fallback 문구
목록(`db.go` `fallbackPhrases`)에서 날짜별로 하나씩 보여준다.

## 구성

| 파일 | 역할 |
|---|---|
| `main.go` | Go 서버 — `web/` 임베드 서빙 + `/api/*`, IAP 헤더/`DEV_USER_EMAIL` identity |
| `db.go` | Postgres 스키마(`english_zoa`) + 프로필·로그인·점수·문구 쿼리 |
| `slack.go` | Data API로 `#learning-english-with-ai` 조회 + 문구 파싱 |
| `web/index.html`, `web/app.jsx`, `web/style.css` | no-build 프론트(React+Babel CDN) |

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

MADANG과 동일한 절차:

1. `#eng-apps-platform-v2`에 앱 이름(`english-zoa`) + 태훈/승헌 이메일을 보내
   신규 앱 배포 권한을 "hotfix"로 열어달라고 요청
2. `#learning-english-with-ai` 채널에 앱을 초대하고, Slack 읽기용 Data API
   스코프(`channels:history`, `channels:read`) 승인을 플랫폼팀에 요청
3. 배포:

```bash
docker build -t english-zoa:latest .
apps-platform app deploy --image english-zoa:latest
```

(`make deploy`가 동일 동작. 로컬 Data API 접근은 `apps-platform app forwarder --service english-zoa`.)

## 검증

```bash
go test ./...   # parsePhraseText, 점수 계산 유닛 테스트
go run .        # 로컬 실행 후 브라우저에서 플래시카드 flip → 알아요/몰라요 → 캘린더 → 리더보드 확인
```
