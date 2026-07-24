# Phase completion report

## 1. 구현 범위

- XPLATFORM XML 4000
- Nexacro XML 4000
- Nexacro JSON 1.0
- handwritten DTO와 `xapi` struct tag 기반 binding
- Parameters, Dataset, ColumnInfo, ConstColumn, Rows, RowType, OrgRow
- Decimal/date-time lexical scalar, Optional/Tracked, BLOB, `xapihttp`

## 2. 설계 결정

- XML은 `encoding/xml` token API와 control-entity adapter를 사용한다.
- JSON은 bounded lexer로 root, metadata, rows를 순차 처리한다.
- compiled schema가 column ID를 field slot으로 매핑하며 row별 linear lookup을 하지 않는다.
- strict 입력은 모호한 multi-dataset property reorder를 거부하고, compatibility 입력만 generic fallback을 사용한다.
- BLOB은 XML/JSON encode와 정상 decode 경로에서 base64 stream/accumulator를 사용한다.
- profile과 options는 `Codec` 생성 시 고정되며 global mutable 설정은 없다.

## 3. 주요 변경 파일

- `codec.go`, `profile.go`, `schema.go`, `scalar.go`, `presence.go`, `row.go`
- `xml_codec.go`, `json_codec.go`, `json_stream.go`, `blob.go`
- `xapihttp/client.go`
- `testdata/`, `*_test.go`, `.github/workflows/ci.yml`
- `docs/clean-room.md`, `docs/protocol-compatibility.md`, `docs/performance.md`, `docs/adr/`, `docs/release-audit.md`

## 4. 테스트 결과

최신 worktree에서 다음 gate가 통과했다.

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `gofmt` 검사
- JSON, JSON BLOB, XML, tag parser, base64 fuzz smoke

Conformance, scalar matrix, control entity, RowType/OrgRow, BLOB boundary,
security-limit, HTTP cancellation/non-2xx 테스트를 포함한다.

## 5. Benchmark 결과

`Apple M4`, Go 1.26, `-benchtime=1x` smoke 기준 최신 측정은 다음과 같다.

| benchmark | time | throughput | allocated | allocs |
|---|---:|---:|---:|---:|
| JSON 100k DTO decode | 72.9 ms | 53.37 MB/s | 144.7 MB | 1,300,081 |
| XML 100k DTO decode | 132.2 ms | 45.31 MB/s | 117.1 MB | 3,301,048 |
| XML 100k tokenization | 87.4 ms | 68.55 MB/s | 75.2 MB | 2,600,071 |
| JSON 32MB BLOB decode | 195.8 ms | 171.37 MB/s | 64.2 MiB | 78 |
| XML 32MB BLOB encode | 12.7 ms | 2,638.15 MB/s | 14.2 KB | 59 |

Benchmark 결과는 allocator와 DTO/reflection 비용을 포함하는 smoke evidence이며,
release target이나 parser 후보 간 우열을 주장하는 수치가 아니다.

## 6. Memory 결과

개별 benchmark process의 `/usr/bin/time -l` peak RSS는 JSON 100k decode
104,513,536 B, XML 100k decode 101,990,400 B, JSON 32MB BLOB decode
168,148,992 B, XML 32MB BLOB encode 102,105,088 B였다. Go test binary와
allocator 기동 비용을 포함하므로 DTO-only peak heap과 동일하지 않다.

## 7. Protocol 불확실성

- vendor runtime/adapter interop는 vendor runtime과 공개되지 않은 artifact가 없어 실행하지 못했다.
- 제3자 XML parser 후보와의 동일 harness 비교는 아직 없다.
- explicit null, charset transcoding, vendor별 control-reference 허용 범위는 compatibility 문서에 미확정으로 남겼다.
- 공개 reference에서 확인된 XML Parameter `value` attribute, root `version`,
  `BIG_DECIMAL`/`DATE_TIME`/`BOOL` alias는 독립 Go 테스트로 검증했다.

## 8. 남은 작업

- Java 17 및 `io.github.clickin:xapi-xml:1.1.6` artifact가 제공되는 환경에서 공개 reference test 재실행
- 동일 fixture 기반 제3자 parser 후보 benchmark
- 필요 시 typed schema builder와 advanced unknown-column capture 추가

## 9. 다음 phase

외부 evidence가 제공되기 전까지는 현재 API와 compatibility matrix를 안정화하고,
vendor-specific 동작을 추측해 추가하지 않는다. 외부 evidence가 확보되면
`docs/release-audit.md`와 parser ADR의 partial 항목을 갱신한다.
