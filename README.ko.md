# context-diary

[English](README.md) | **한국어**

[![CI](https://github.com/tae2089/context-diary/actions/workflows/ci.yml/badge.svg)](https://github.com/tae2089/context-diary/actions/workflows/ci.yml)

모든 코드 변경의 **왜(why)** 를 기록하고 — 팀의 누구든 자연어로 물어볼 수 있게.

`git log`는 *무엇이* 바뀌었는지 알려줍니다. 코드 리뷰는 *바꿔도 되는지*
알려줍니다. 그런데 6개월 뒤엔 아무도 *왜* 바뀌었는지 기억하지 못합니다.
context-diary는 그 맥락을 커밋 시점에 기록하고, `git log`를 한 번도
실행해보지 않을 사람까지 포함해 누구나 조회할 수 있게 만듭니다.

## 동작 방식

git이 진실의 원천입니다. 서버는 읽기 전용 인덱스일 뿐입니다.

1. **컨벤션 + 훅** — 커밋이 [git 트레일러](docs/trailer-format.ko.md)
   (`Context-Why`, `Context-Scope`, …) 형태로 구조화된 맥락을 담습니다.
   AI 코딩 에이전트(Claude Code, Codex 등)가 컨벤션 스니펫을 보고 직접
   작성하고, 린트 훅이 이를 강제하며 위반 내용을 돌려줘 에이전트가
   스스로 고치게 합니다. 사람에게는 커밋 에디터에 템플릿이 제공됩니다.
   API 호출 없음 — 전부 로컬에서 동작합니다.
2. **인덱서** — `context-diary index`가 기본 브랜치의 커밋 트레일러를
   Postgres에 적재합니다 (로컬/크론/CI 어디서든). 데이터베이스는 버려도
   되는 읽기 모델입니다 — 지우고 재인덱싱하면 git에서 전부 복원됩니다.
3. **서버** (`context-diary serve`) — 조직 전체를 위한 단일 배포:
   - GitHub PR 봇: Atlantis 스타일로 PR을 검토하고(봇 코멘트 1개:
     통과 시 인덱스 미리보기, 실패 시 템플릿), 브랜치 보호로 필수 지정
     가능한 `context-diary/context` 커밋 상태를 세팅하며, 머지를
     비동기로 인덱싱하고 머지 커밋에 `context-diary/ingest` 상태
     (pending → success)를 남깁니다;
   - MCP 엔드포인트 (`/mcp`): `search_context` / `list_scopes` /
     `explain_function` / `related_by_ref` 툴 제공 — 비개발자를 포함한
     누구나 자신의 AI 어시스턴트에서 "주문 취소가 왜 이렇게 동작해요?"
     라고 물으면 눈높이에 맞게 번역된 답을 받습니다;
   - 읽기 전용 웹 UI (`/ui/`): AI 어시스턴트 없이도 브라우저로
     인덱스를 검색·탐색할 수 있습니다.

## 현재 상태

[트레일러 포맷 스펙](docs/trailer-format.ko.md)과 `context-diary` CLI
(훅, 린트, 에이전트 설정), 인덱서, 서버(PR 봇 + MCP + 웹 UI)가 모두
동작합니다.

```sh
go install github.com/tae2089/context-diary/cmd/context-diary@latest
cd your-repo
context-diary init --agent claude-code   # 훅 + 설정 + CLAUDE.md 스니펫
```

로드맵:

- [x] 트레일러 포맷 스펙 (v0.1)
- [x] 커밋 훅 / CLI (`context-diary` 바이너리)
- [x] 인덱서 (`context-diary index` → Postgres)
- [x] 서버: GitHub PR 봇 + MCP 엔드포인트 (`context-diary serve`)
- [x] GitHub App 인증 (PAT도 계속 지원; 배포 환경엔 App 권장)
- [x] 백필: [git notes](docs/backfill.ko.md)로 도입 이전 히스토리에 맥락 부여
- [x] 웹 UI (serve의 `/ui/` — 검색·스코프 탐색; 읽기 전용, JS 없음)

`serve`는 의도적으로 단일 인스턴스입니다(인메모리 큐, 로컬 미러 캐시) —
셀프호스트 OSS 배포에 맞는 트레이드오프입니다. `/mcp`에 bearer 토큰을
요구하려면 `CONTEXT_DIARY_MCP_TOKEN`을 설정하세요.

## 머지 전략

트레일러는 기본 브랜치까지 살아남아야 합니다. PR 봇은 두 가지 캐리어를
모두 검사하며 하나만 통과하면 됩니다: PR 본문의 트레일러(squash 팀),
또는 모든 non-merge 브랜치 커밋의 트레일러(merge/rebase 팀). 유지하고
싶은 맥락의 정밀도에 맞춰 행을 고르세요:

| 머지 전략 | 맥락 정밀도 | 할 일 |
| --- | --- | --- |
| Merge commit (맥락 최대 보존 — AI 에이전트가 커밋을 작성하는 레포에 최적) | 브랜치 커밋 각각 | 커밋이 트레일러를 담습니다(훅 + 에이전트가 처리). 인덱스와 서버를 `--walk full`로 실행하세요. 머지 커밋 자체는 트레일러가 없어도 됩니다 — 변경이 아니라 박음질이니까요. |
| Squash merge (규율 최소 — 사람 위주 팀에 최적) | PR당 엔트리 1개 | PR 본문에 트레일러를 쓰고 squash 기본 메시지를 **"Pull request title and description"** 으로 설정하세요. 브랜치의 WIP 커밋은 아무것도 필요 없습니다 — 어차피 버려집니다. |
| Rebase merge | 브랜치 커밋 각각 | 커밋이 그대로 올라갑니다. merge commit과 같은 커밋 단위 규율에 더해 깨끗한 히스토리 습관(WIP 커밋 없음)이 필요합니다. |

머지 전 강제는 실행 중인 `serve`의 `context-diary/context` 필수 상태로,
서버가 없는 팀은 [PR 린트 액션](examples/github-actions/pr-context-lint.yml)
(PR 본문 경로만)으로 하세요. 어느 쪽이든 기본 브랜치에서 도는 CI의
`context-diary lint`가 빠져나간 것을 잡는 최종 안전망입니다.

## 설계 원칙

- **git이 진실의 원천.** 서버는 언제든 히스토리에서 재구축 가능하며,
  서버를 잃어도 아무것도 잃지 않습니다.
- **한 번만, 개발자 눈높이로 작성.** 청중별 번역(기획자·CS용)은 작성
  시점이 아니라 조회 시점에 AI가 합니다.
- **셀프호스트.** 훅·인덱싱·서빙이 바이너리 하나. 저장소는 Postgres이며
  인덱스는 버려도 재구축되므로 백업이 필요 없습니다.

## 라이선스

[MIT](LICENSE)
