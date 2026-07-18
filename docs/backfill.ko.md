# 백필 v0.1 — 도입 이전 히스토리에 맥락 부여

[English](backfill.md) | **한국어** — 해석이 갈릴 경우 영어판이 기준입니다.

상태: Draft
의존: [트레일러 포맷 v0.1](trailer-format.ko.md), [인덱서 설계 v0.1](indexer-design.md)

## 목적

도입하는 저장소에는 `Context-*` 트레일러가 없는 수년치 커밋이 있어서,
기존 코드에 대해서는 인덱스가 비어 있습니다. 백필은 **히스토리를
리라이트하지 않고** [git notes](https://git-scm.com/docs/git-notes)의
전용 ref로 그 커밋들에 맥락을 붙입니다:

```
refs/notes/context-diary
```

노트 내용은 순수 트레일러 블록입니다 (마지막-문단 규칙 없음 — 노트
전체가 트레일러):

```
Context-Why: retry queue was added after the 2023 payment outage
Context-Scope: payment/retry
Context-Decision: at-least-once delivery over exactly-once; consumer is idempotent
```

## 우선순위

작성된 커밋 트레일러가 항상 이깁니다. 노트는 커밋 메시지에 비어 있지
않은 `Context-Why`가 없을 때만 참조됩니다. 따라서 노트를 편집해도
작성자가 커밋 시점에 남긴 맥락을 덮어쓸 수 없습니다.

## 워크플로우 (AI 에이전트 주도)

생성은 커밋 작성과 같은 원칙으로 에이전트에 위임됩니다: 도구는 후보를
찾고 결과를 인덱싱하며, AI 코딩 에이전트(Claude Code, Codex 등)가
히스토리를 읽고 노트를 작성합니다.

```sh
# 1. 맥락 없는 커밋 나열 (해시<TAB>제목, 오래된 것부터)
context-diary backfill

# 2. 후보마다 에이전트가 변경을 살펴보고 노트 작성
git show <hash>
git notes --ref=context-diary add -m 'Context-Why: <이유>
Context-Scope: <스코프>' <hash>

# 3. 노트 공유 (기본으로는 push되지 않음)
git push origin refs/notes/context-diary

# 4. 재인덱스: --rescan은 커서를 무시하고, 내용이 같은 커밋은
#    no-op이므로 몇 번을 반복해도 안전
context-diary index --rescan
```

다른 클론에서 노트 받기:

```sh
git fetch origin refs/notes/context-diary:refs/notes/context-diary
```

에이전트 프롬프트 권장: 오래된 것부터 처리; diff를 다시 서술하지 말고
커밋이 풀었던 문제를 서술 (지시문 스니펫과 같은 품질 기준); diff와 주변
히스토리에서 동기를 복원할 수 없으면 확신을 지어내지 말고 사실적 가설로
표시("likely …").

## serve와의 상호작용

미러는 `--mirror` 시맨틱으로 클론되므로 `refs/notes/*`가 매 fetch에
따라옵니다. 다만 인제스트 커서는 이미 인덱싱된 커밋을 건너뛰므로, 백필
세션 후에는 미러(또는 노트 ref가 있는 아무 클론)에서
`context-diary index --rescan`을 한 번 실행하세요. 커밋이 인덱싱되기
*전에* 도착한 노트는 머지 트리거 인제스트가 자동으로 반영합니다.

## 한계

- 인덱싱 이후의 노트 편집은 `--rescan`을 해야 반영됩니다 (notes 웹훅
  없음).
- GitHub UI는 노트를 렌더링하지 않습니다 — 백필된 맥락은 인덱스와 MCP
  답변에서 보입니다.

## FAQ

**Q. 백필 노트가 모든 과거 커밋을 커버해야 하나요?**
아니요 — 커버리지는 점진적입니다. `context-diary backfill`이 남은 것을
항상 보여주니, 사람들이 실제로 물어보는 커밋부터 달아 나가세요.

**Q. 노트는 누가 써야 하나요?**
저장소를 체크아웃한 AI 코딩 에이전트 — 커밋 작성과 같은 위임 원칙입니다.
후보 목록과 docs/trailer-format.ko.md를 에이전트에 넘기세요.
