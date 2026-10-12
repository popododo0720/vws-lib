# 에이전트 규칙

이 저장소는 VWS 서비스가 공유하는 Go 라이브러리(`obs`, `errs`)만 다룬다. 설계는 https://github.com/popododo0720/vws-deploy/issues/42 이고 구현 작업은 https://github.com/popododo0720/vws-deploy/issues/44 에서 추적한다. 모듈은 `github.com/popododo0720/vws-lib`다.

## 규칙

| ID | 규칙 | 구조 | 검사 |
| --- | --- | --- | --- |
| L1 | PR은 main을 대상으로 하고 본문에 `Closes #N`이 있으며, main에 PR 없이 들어온 커밋은 CI가 알린다. AGENTS.md는 Insider-risk guardrails 1~7을 갖는다 | `.github/workflows/repo-guard.yml` `.github/workflows/direct-push-watch.yml` `scripts/guard-agents.sh` | `scripts/guard-agents.sh agents` |
| L2 | Cursor 클라우드 에이전트를 쓰지 않는다 | `AGENTS.md` | 판단 |
| L3 | 이 모듈은 vws-proto와 서비스 레포(compute, storage, network, console-api)를 import하지 않는다. 의존 방향은 서비스 → lib 한쪽이다 | `go.mod` `scripts/check-imports.sh` | `scripts/check-imports.sh` (CI의 `imports` 작업) |
| L4 | 코드는 gofmt, goimports를 지키고 `.golangci.yml`의 린트를 통과한다. `std-error-handling` 프리셋은 쓰지 않는다 | `.golangci.yml` | CI의 `golangci-lint` v2.14.0 |
| L5 | `go vet ./...`와 `go test -race ./...`가 통과한다 | `.github/workflows/ci.yml` | CI |
| L6 | 가드 파일 3개(`repo-guard.yml`, `direct-push-watch.yml`, `scripts/guard-agents.sh`)는 vws-deploy main과 바이트 단위로 같다. 이 저장소에서 고치지 않는다 | - | 판단 (`sha256sum`으로 비교) |

표의 검사 칸이 판단인 규칙은 작성자와 검토자가 지킨다.

## 브랜치와 원자적 커밋

- main에서 딴 독립 브랜치에서 작업하고 PR 하나로 올린다. 스택 PR을 만들지 않는다. main에 직접 푸시하지 않는다(저장소를 만든 첫 구조 커밋만 예외였다).
- 커밋 하나는 한 가지 변경이다. 제목은 `scope: 요약`처럼 짧게 쓴다. 커밋마다 `go build ./...`와 `go test ./...`가 통과해야 한다.
- PR 본문에 `Closes #N`을 쓴다. 이슈는 vws-deploy#44처럼 다른 레포에 있어도 된다. 다른 저장소의 이슈면 전체 URL을 같이 쓴다.
- 작성 봇은 자기 PR을 머지하지 않는다.
- 태그(`v0.x.y`)는 머지된 green CI main 커밋에 annotated tag로, 작성 봇이 아닌 오케스트레이터나 사람이 발행한다. 소비 레포는 태그 내용을 `third_party/vws-lib`에 복사하고 `replace`로 연결한다.

## 작업 방식 (poteto-mode)

- 간결하게 쓰고 코드는 가장 작은 변경으로 푼다. 추상화 계층을 늘리지 않고 지울 수 있으면 지운다.
- 코드를 쓰기 전에 데이터 모양을 먼저 정한다. 같은 실수가 두 번 나오면 문장 대신 구조나 검사로 막는다.
- 실제로 돌린 것만 검증했다고 쓴다. 돌리지 못한 것은 "미검증"으로 표시한다.
- 커밋 메시지와 PR 설명은 짧은 평서문으로 쓴다. 근거 없는 주장과 만들어낸 링크를 넣지 않는다.
- Cursor 클라우드 에이전트(CloudAgent)를 쓰지 않는다. 작업은 `gh`와 `git`으로 한다.
- 비밀과 계정 ID를 코드, 테스트, 로그에 쓰지 않는다.

## Insider-risk guardrails

봇도 내부자 위험이다. 아래는 예외 없는 규칙이다.

1. **검증 독립**: 중요 결과(머지, 보안, 데이터)는 작성한 봇/모델이 스스로 검증하지 않는다. 검증은 작성 봇이 아닌 다른 봇(또는 사람)이 한다. (프로세스 규칙, CI로 강제 불가)
2. **기록**: 모든 변경은 이슈 → PR(`Closes #N`) → 커밋으로 남긴다. 작업 로그는 PR 본문/이슈 코멘트에 사람이 읽을 수 있게 쓴다. force-push·히스토리 재작성 금지(사용자 승인 시 예외).
3. **실패 테스트**: 새 기능은 정상 경로만 테스트하지 않는다. 전원 차단/재시작, 멱등 재실행, 악성·경계 입력 케이스를 최소 1개씩 포함하거나 불가 사유를 PR에 적는다.
4. **최소 권한**: main 직접 푸시 금지. 모든 변경은 main에서 딴 독립 브랜치의 단일 PR. 스택 PR 금지. 로컬 머지 금지. 토큰은 해당 레포 범위만. RBAC은 서비스 코드가 실제 호출하는 리소스만, 가능하면 Role(네임스페이스 범위).
5. **감사 분리**: 작성 봇 ≠ 검증 봇 ≠ 머지 판단. 작성 봇은 자기 PR을 머지하지 않는다. 머지는 base==main 이고 head 커밋 CI가 전부 success일 때만.
6. **격리·비상정지**: 봇/에이전트 즉시 중단은 사용자 요청 또는 오케스트레이터가 해당 봇 세션 중단 + 토큰 폐기(`gh auth`/PAT revoke). 위험 작업은 샌드박스(박스/임시 클러스터)에서만. 운영 클러스터 접근 금지.
7. **사고 공개**: 규칙 위반·사고는 숨기지 않고 이슈에 `원인 / 영향 / 조치 / 재발방지`로 기록한다 (라벨 `incident`).

이 레포의 서버측 보호는 없다(private, 무료 플랜). 1, 5, 6은 프로세스 규칙이고, 2와 4의 일부는 `.github/workflows/repo-guard.yml`이 PR의 base와 `Closes #N`을 검사하고 `.github/workflows/direct-push-watch.yml`이 main에 PR 없이 들어온 커밋을 사후에 알린다. 차단이 아니라 탐지다.
