## Do

1. 코드 분석과 진입점을 찾을때는 ccg mcp를 활용한다.
2. **git commit 시 반드시 context 트레일러를 넣는다** (아래 "커밋 컨벤션").

## 커밋 컨벤션 (필수)

커밋을 만들 때 메시지 **마지막 문단**에 context 트레일러 블록을 넣는다.
이걸 빠뜨리면 해당 커밋은 인덱싱되지 않아 "왜 이렇게 했는지"가 유실된다.

- `Context-Why:` (필수, 1개) — 이 변경이 존재하는 **이유** 한 줄. 무엇을
  바꿨는지 재서술 금지 — 문제/동기를 쓴다. ("fix bug" 같은 건 무효.)
- `Context-Scope:` (선택, 반복) — 제품 개념 슬러그. 소문자 `/` 구분,
  예: `serve/admin`. `.context-diary.toml`의 scopes 목록서 우선 선택.
- `Context-Decision:` (선택, 반복) — 주요 트레이드오프. `택한 것 over 기각한
  것; 이유` 형태 권장.
- `Context-Ref:` (선택, 반복) — 관련 이슈/ADR/장애/문서의 URL 또는 ID,
  혹은 코드 ref (`owner/repo:path#Symbol`).

값은 각각 한 줄. 긴 설명은 트레일러 위 본문에 쓴다. 개발자 눈높이로 작성.

예시:

```
feat: delay refund until PG settlement confirmed

즉시 환불이 PG 정산 대기와 경쟁해 이중 환불 발생. 정산 웹훅을 기다리도록 변경.

Context-Why: instant refund raced with pending PG settlement, causing double refunds
Context-Scope: payment/refund
Context-Decision: settlement-webhook trigger over PG status polling; webhook already delivers the event
Context-Ref: https://github.com/example/shop/issues/123
```

머지 전략별 주의:
- **squash 머지 repo**: 같은 트레일러를 PR 설명 마지막 문단에도 넣는다
  (squash 커밋 메시지가 여기서 조합됨).
- **merge-commit 머지 repo**: 브랜치의 각 커밋에 트레일러가 있어야 한다.
  머지 커밋 자체엔 트레일러가 없으므로, 서버 인덱서는 `--walk full`로
  돌아야 브랜치 커밋을 수집한다 (first-parent 워크는 이들을 건너뜀).

전체 규칙은 `context-diary instructions` 출력 또는 docs/trailer-format.md 참고.

## ccg 활용

ccg mcp에서 search를 활용하여 빠르게 진입점을 찾는다.
ccg 사용시, namespace는 context-diary를, repo-root는 /repos/context-diary로 한다.
