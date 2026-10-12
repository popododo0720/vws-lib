# 태그 발행 절차 (v0.1.0부터)

태그는 **dr eggbot(오케스트레이터)이나 사람만** 발행한다. 작성 봇은 태그를 만들거나 지우거나 옮기지 않는다(AGENTS.md "브랜치와 원자적 커밋"). 이 문서는 절차일 뿐이고 이 문서를 머지해도 태그는 생기지 않는다.

## 버전 규칙

- `v0.x.y`. 0.x 동안 공개 API(`obs`, `errs`와 하위 패키지의 내보낸 이름, 환경 변수 이름, 메트릭 이름과 레이블)가 바뀌면 `x`를 올리고, 호환 수정이면 `y`를 올린다.
- 메트릭 이름, 레이블, 로그 필드, 환경 변수 이름을 바꾸는 것은 대시보드와 차트를 깨므로 API 변경으로 친다(`x`).
- 태그는 옮기지 않는다. 잘못 발행했으면 새 `y`를 발행한다. 소비 레포의 `vendor-lib.sh check`는 옮겨진 태그를 실패로 본다.

## v0.1.0에 들어가야 하는 것

vws-deploy#44의 항목 (a)~(d) PR이 main에 머지되고 그 main의 CI가 green이어야 한다.

- [ ] (a) obs, errs 구현
- [ ] (b) `scripts/check-imports.sh`
- [ ] (c) 샘플 소비자 빌드(`scripts/check-consumer.sh`), 샘플이 (a)의 API를 부르도록 갱신
- [ ] (d) `scripts/vendor-lib.sh`

## 발행 절차 (발행자)

1. `git fetch origin` 후 발행할 커밋 `C`를 정한다. `C`는 `origin/main`의 커밋이고 PR로 들어왔어야 한다(`direct-push-watch`가 실패한 커밋은 안 된다).
2. `C`의 CI가 모두 success이고 skipped가 없는지 확인한다: `gh run list --commit C`.
3. 로컬에서 같은 검사를 돌린다: `go vet ./...`, `go test -race ./...`, `golangci-lint run ./...`, `scripts/guard-agents.sh selftest`, 그리고 (b)~(d)의 스크립트와 selftest.
4. annotated tag를 만든다: `git tag -a v0.1.0 C -m "vws-lib v0.1.0"` 후 `git push origin v0.1.0`. 브랜치는 푸시하지 않는다.
5. GitHub 릴리스 노트에 적는다: 커밋, 바뀐 공개 API, 의존 모듈과 버전(1.x/0.x 안정성 표), 미검증 항목.
6. vws-deploy#44에 태그와 커밋을 댓글로 남긴다.

## 소비 레포 갱신 (각 서비스 레포의 작성 봇, PR로)

1. `scripts/vendor-lib.sh vendor v0.1.0` → `third_party/vws-lib`와 `VENDOR`가 바뀐다.
2. `go.mod`에 `require github.com/popododo0720/vws-lib v0.0.0`과 `replace ... => ./third_party/vws-lib`가 있는지 확인하고 `go mod tidy`.
3. 소비 레포 CI에 `scripts/vendor-lib.sh check`를 둔다.
4. PR 본문에 태그, 커밋, 바뀐 API와 그에 따른 코드 수정을 적는다.

## 확인 필요

- 소비 레포 CI가 private 레포를 읽는 방법(토큰 범위). 정해지기 전에는 `VWS_LIB_OFFLINE=1`로 해시만 검사할 수 있다.
- 레포가 공개되면 `replace`와 벤더링을 버리고 태그를 직접 `require`할지.
