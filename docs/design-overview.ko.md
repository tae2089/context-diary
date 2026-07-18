# 설계 개요 (한국어 요약)

[README.ko.md](../README.ko.md)로 돌아가기

설계 문서는 변경이 잦아 영어판만 상세를 유지합니다. 이 문서는 세 설계
문서의 핵심을 요약합니다 — 상세와 최신 내용은 각 영어판이 기준입니다.

---

## CLI & 커밋 훅 — [cli-design.md](cli-design.md)

트레일러 작성을 사실상 공짜로 만드는 계층. 바이너리 하나
(`context-diary`)에 모든 서브커맨드가 있습니다.

**불변식:**

- **I-1 절대 커밋을 막지 않음** — 도구 실패(설정 없음, IO 에러)는 stderr
  경고 + exit 0. 유일한 예외는 opt-in인 `lint.level = "strict"`.
- **I-2 로컬 전용** — 네트워크 호출 없음. 저장소의 어떤 것도 머신을
  떠나지 않음.
- **I-3 남의 파일을 편집하지 않음** — `init`은 자기 마커가 있는 파일만
  갱신하거나 새로 만들고, 그 외에는 수동 안내를 출력.

**핵심 결정:** 트레일러 생성은 LLM API 호출이 아니라 **커밋을 작성하는
AI 에이전트에 위임** (v0.2에서 전환). 훅은 사람에게 템플릿을,
에이전트에게는 린트 거부→재시도 루프를 제공. `hook.mode = comment|off`,
`lint.level = warn|strict`. 설정 파일에 시크릿 금지.

**명령:** `init [--agent claude-code|codex]`, `instructions`,
`hook prepare-commit-msg|commit-msg`, `lint <범위>`, `lint-message`,
`backfill`, `explain <파일> <함수>`, `index`, `serve`, `scopes`.

---

## 인덱서 — [indexer-design.md](indexer-design.md)

git 히스토리를 조회 가능한 읽기 모델로. **DB는 버려도 됩니다** — git이
진실의 원천이고, 지우고 `index`를 다시 돌리면 동일하게 복원됩니다.

- 저장소: **Postgres 전용** (프로젝트 결정; FTS + pg_trgm으로 한국어
  검색 포함).
- 증분: 레포별 커서. `--rescan`은 커서를 무시하고 파생 데이터를 강제
  재생성 (파서 업그레이드·백필 노트 반영용).
- 걷기 모드: `--walk first-parent`(기본, squash/rebase 팀) |
  `--walk full`(전체 DAG, merge commit 팀).
- 스키마 축: 스코프 / 시간 / 자유 텍스트(FTS+trigram) / 코드 ref
  역참조. 배치 삽입 + 커서 갱신이 한 트랜잭션 — 크래시해도 커밋 누락
  불가, 최악이 중복 제거되는 재스캔.

---

## 서버 — [serve-design.md](serve-design.md)

조직 전체용 단일 배포 (단일 인스턴스 설계).

```
context-diary serve
├─ POST /webhook/github   PR 이벤트 → 이중 경로 검증(본문 or 전 커밋),
│                         봇 코멘트 1개 유지 + context-diary/context 상태
│                         머지 → 비동기 큐 → 미러 fetch → 인제스트
│                         + context-diary/ingest 상태 (pending→success)
├─ /checks/{id}           Atlantis 스타일 체크 상세 페이지 (인메모리)
├─ /mcp                   MCP (공식 Go SDK): search_context, list_scopes,
│                         explain_function, related_by_ref
├─ /ui/                   읽기 전용 웹 UI (서버 렌더링, JS 없음)
└─ /healthz
```

- **인증:** `GITHUB_TOKEN`(PAT) 또는 GitHub App
  (`GITHUB_APP_ID`/`GITHUB_APP_INSTALLATION_ID`/`GITHUB_APP_PRIVATE_KEY(_FILE)`,
  배포 권장). `/mcp`는 `CONTEXT_DIARY_MCP_TOKEN` 설정 시 bearer 필수.
- **큐:** 인메모리, 용량 256, 워커 4, 레포 단위 직렬화. 재시작 시 대기
  작업 유실 — 커서가 다음 머지 때 무손실로 따라잡음 (Atlantis와 같은
  트레이드).
- **웹훅은 즉시 202** — GitHub은 실패한 웹훅을 자동 재전달하지 않으며
  타임아웃이 10초라, 첫 클론이 오래 걸릴 수 있는 인제스트는 반드시
  비동기.
