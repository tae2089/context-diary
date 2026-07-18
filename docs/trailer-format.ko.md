# Context 트레일러 포맷 v0.1

[English](trailer-format.md) | **한국어** — 해석이 갈릴 경우 영어판이 기준입니다.

상태: Draft
라이선스: MIT

## 목적

context-diary는 코드 변경의 **왜**를 변경이 일어나는 순간에
[git 트레일러](https://git-scm.com/docs/git-interpret-trailers)로 커밋
메시지에 기록합니다. git이 유일한 진실의 원천이며, 인덱서와 MCP 서버는
이 포맷이 정의한 것만 읽습니다.

이 문서는 다음 둘 사이의 계약입니다:

- **작성자(writer)** — 커밋 메시지를 쓰는 개발자(또는 AI 커밋 훅),
- **독자(reader)** — 기본 브랜치의 머지된 커밋을 파싱하는 인덱서.

## 위치

트레일러는 git 규칙에 따라 커밋 메시지의 마지막 문단에 두어야 합니다
(MUST). 권장 레이아웃:

```
<제목 줄>

<본문: 자유 서술, 길어도 됨>

<트레일러 블록>
```

본문은 긴 서사를, 트레일러는 기계가 조회 가능한 구조화 요약을 담습니다.
인덱서는 둘 다 저장합니다.

## 문법

- 키: `Context-` 접두어 + 등록된 이름. 읽을 때는 대소문자 무시, 쓸 때는
  아래 표의 표준 표기를 권장(SHOULD).
- 구분자: `: ` (콜론 + 공백).
- 값: UTF-8 한 줄. 여러 줄 값은 **금지** — 이어쓰기 줄 지원이 git 도구마다
  제각각이기 때문입니다. 한 줄로 부족하면 상세는 본문에 쓰고 트레일러는
  요약으로 유지하세요.
- 미등록 `Context-*` 키는 독자가 조용히 무시해야 하며(MUST, 전방
  호환성), 파싱 실패를 일으켜서는 안 됩니다(MUST NOT).

## 키 레지스트리

| 키                 | 필수 | 반복 | 값                                                             |
| ------------------ | ---- | ---- | -------------------------------------------------------------- |
| `Context-Why`      | 예   | 아니오 | 이 변경이 존재하는 이유 한 줄. 개발자 눈높이로 작성.           |
| `Context-Scope`    | 아니오 | 예 | 변경이 속한 기능/도메인 슬러그. 아래 문법 참고.                |
| `Context-Decision` | 아니오 | 예 | 내린 결정. `선택한 것 over 기각한 것; 이유` 형태 권장.          |
| `Context-Ref`      | 아니오 | 예 | 관련 자료의 URL 또는 식별자 (이슈, ADR, 장애, 문서).            |

필수 키는 `Context-Why` 하나뿐입니다. `Context-Why`가 없는 커밋은 그저
맥락 엔트리로 인덱싱되지 않을 뿐 — 에러가 아닙니다.

### 스코프 슬러그 문법

```
scope   = segment *( "/" segment )
segment = 1*( 소문자 / 숫자 / "-" )
```

예: `order/cancel`, `payment/refund`, `auth`. 스코프는 비개발자 질의
("주문 취소가 왜 이렇게 동작해요?")의 1차 조회 축이므로 코드 경로가
아니라 **제품 개념**으로 이름 짓습니다. 팀은 공유 스코프 목록을
유지해야 하며(SHOULD), 인덱서는 처음 보는 스코프를 새 스코프로
취급합니다.

## Ref의 세 가지 형태

`Context-Ref` 값은 자유 텍스트 한 줄이지만, 독자는 세 형태를 해석합니다
(additive — 구버전 독자는 전부 불투명한 텍스트로 취급):

| 형태 | 예시 | 독자 동작 |
| --- | --- | --- |
| URL | `https://wiki.example.com/postmortem-42` | 텍스트 검색 조인 키 |
| 이슈 ID | `JIRA-123` | 텍스트 검색 조인 키 |
| 코드 ref | `owner/repo:path/to/file.go#Symbol` | 구조화 저장: 역참조 가능 ("어느 레포의 어떤 엔트리가 이 함수를 참조하나") |

코드 ref 문법: 저장소 이름, 리터럴 `:`, 파일 경로, 선택적 `#Symbol`
(함수/메서드 이름)입니다. GitHub blob URL도 코드 ref로 파싱됩니다 (레포+경로만 —
`#L10` 라인 번호는 편집에 취약하므로 무시). 기존
`owner/repo//path/to/file.go#Symbol` 형식은 이미 작성된 커밋 기록을 위해
계속 읽지만, 새 ref에는 콜론 형식을 사용하세요. ref를 레포 간 조인으로
활용하세요: 서로 다른 레포의 엔트리가 같은 티켓 URL을 공유하거나 서로의
함수를 가리키면 하나의 추적 가능한 작업이 됩니다.

## 레포를 넘나드는 스코프

스코프는 제품 개념이므로 의도적으로 레포 경계를 넘습니다:
order-service와 payment-service 양쪽의 `payment/refund`는 **같은**
스코프이며, 스코프 질의는 레포를 가로지릅니다. 조직 전체가 **하나의**
공유 스코프 사전을 유지하세요 (각 레포 `.context-diary.toml`의 scopes
목록을 여기서 가져오도록) — 슬러그가 어긋나면 (`payment/refund` vs
`refunds`) 조인이 깨집니다.

## 청중 수준

트레일러 값은 **개발자** 언어 수준으로 작성합니다. 커밋 시점에 두 벌
(개발자용 + 비개발자용)을 쓰지 마세요 — 청중 번역은 조회 계층(AI)의
일입니다. 작성 비용을 낮추고 두 문장이 어긋나는 사고를 막습니다.

## 무엇을 어디에

| 위치          | 내용                                                        |
| ------------- | ----------------------------------------------------------- |
| 트레일러      | 구조화 요약: why, 스코프, 결정, ref. 커밋 단위.              |
| 커밋 본문     | 트레일러 뒤에 있는 긴 서사. 커밋 단위.                       |
| PR 본문       | 커밋을 가로지르는 논의, 리뷰 맥락. 머지 시점에 인덱싱.        |
| 동반 문서     | 오래가는 설계 기록(ADR). `Context-Ref`로 연결.               |

## 예시

```
fix(order): delay refund until PG settlement is confirmed

Refunds fired immediately on cancellation caused double refunds when the
PG settlement was still pending. The refund now waits for the settlement
webhook before executing. Considered polling the PG status API instead,
but the webhook already carries the settlement event and polling would
add a scheduler dependency.

Context-Why: instant refund raced with pending PG settlement, causing double refunds
Context-Scope: order/cancel
Context-Scope: payment/refund
Context-Decision: settlement-webhook trigger over PG status polling; webhook already delivers the event
Context-Ref: https://github.com/example/shop/issues/123
```

값은 한국어로 써도 됩니다 — 자유 텍스트이며 검색(trigram)도 한국어를
지원합니다. 팀 컨벤션의 문제일 뿐입니다.

## 파싱 노트 (구현자용)

- 기준 시맨틱: `git interpret-trailers --parse`. git이 트레일러로 파싱하는
  것은 독자도 받아들이고(MUST) 위 레지스트리 규칙을 적용합니다.
- go-git에는 트레일러 파서가 없으므로 인덱서가 이 문법을 직접 구현합니다.
  독자는 메시지 **끝의 연속된** 전체-트레일러 문단들을 하나의 트레일러
  블록으로 취급합니다 — git의 "마지막 문단" 규칙보다 관대하며, 이는
  의도적입니다: GitHub의 squash 머지는 `Co-authored-by`를 별도의 마지막
  문단으로 덧붙이는데, 그러면 PR 본문에 쓴 Context 트레일러가 본문으로
  밀려나 조용히 인덱싱에서 빠집니다 (첫 squash 머지 PR에서 실제로 관측).
- 라인 단위 코드↔커밋 매핑(`git log -L` 상당)은 이 포맷의 범위 밖 —
  인덱서의 관심사입니다.

## 버전 관리

이 스펙은 제목의 버전으로 관리됩니다. 추가적 변경(새 선택 키)은 버전을
올리지 않습니다. 파괴적 변경(기존 키의 의미 변경)은 올립니다.
