# Release readiness audit

이 문서는 `xapi-go-agent-master-prompt.md`의 최종 합격 기준을 현재 worktree의
실제 evidence에 대조한 기록이다. `pass`는 코드와 테스트가 해당 범위를 직접
검증한다는 뜻이고, `partial`은 동작은 있으나 요구한 증거 또는 전체 범위가
아직 부족하다는 뜻이다.

| 기준 | 상태 | evidence / gap |
|---|---|---|
| 동일 handwritten DTO로 XML/JSON 사용 | pass | `codec_test.go`, `conformance_test.go` |
| string column lookup 제거 | pass | typed tag API; dynamic Dataset API 없음 |
| DECIMAL lexical precision | pass | `Decimal`, `scalar_matrix_test.go` |
| lexical/custom scalar hook | pass | `LexicalValue`, `encoding.TextMarshaler`/`TextUnmarshaler` path |
| 모든 위치의 base64 BLOB | pass | parameter/const/row/orgrow tests, `BlobSource`, strict/compatibility whitespace policy |
| ASCII control entity single-pass | pass | control/CDATA tests in `codec_test.go` |
| missing/empty/null 구분 | pass | `Optional`, JSON presence tests; non-optional null policy는 단순 zero |
| column order 독립 | pass | XML/JSON reordered-column tests |
| ColumnInfo contract metadata | pass | strict wire type/duplicate/column-limit tests |
| empty Dataset ColumnInfo | pass | `TestEmptyDatasetWritesSchema` |
| RowType/OrgRow typed model | pass | XML and JSON U/O fixtures/tests |
| XML/JSON streaming core | partial | XML events and JSON bounded lexer cover normal DTO decode and unambiguous reordered DTOs; strict mode rejects ambiguous multi-dataset order, while compatibility mode retains a documented generic fallback |
| 대형 BLOB double buffering 없음 | pass (normal/strict path) | XML Parameter/ConstColumn/Col/OrgRow and JSON writer/lexer use direct base64 streams or accumulators; compatibility-only ambiguous fallback may materialize values |
| immutable concurrent Codec | pass | options are per Codec; `go test -race ./...` |
| strict/compatibility separation | pass | `WithStrictMode`, `WithCompatibility`, unknown/missing/duplicate, decimal and JSON-null policies |
| security limits | pass | document/depth/rows/columns/scalar/blob, DTD and duplicate-key tests |
| 100k benchmark/memory documentation | pass | `fuzz_test.go`, `docs/performance.md` |
| parser ADR | partial | `docs/adr/0001-xml-parser-selection.md`; standard-library parser evidence is measured, cross-parser comparison is not |
| race gate | pass | CI and local `go test -race ./...` |
| clean-room/public docs | pass | `docs/clean-room.md`, README, protocol matrix |

## Explicitly deferred

- vendor runtime/adapter interop fixture: public documentation fixtures exist, but
  no vendor runtime or non-public adapter is available in this repository;
- full 10k/100k XML and JSON throughput/peak-RSS comparison across parser candidates;
- optional typed schema builder and advanced unknown-column capture API.

Public reference revisions reviewed for wire-shape evidence are recorded in
`docs/clean-room.md`; the XML Parameter `value` attribute and type spelling aliases
are covered by independent Go tests without copying reference source.
Reference repository tests were attempted from the pinned checkout. A Java 25
toolchain override reached compilation, but the build could not resolve its
`io.github.clickin:xapi-xml:1.1.6` artifact from the configured Maven repository;
the corresponding GitHub repository is not public. This is an external artifact
limitation, not evidence of vendor-runtime interop.

These are not silently treated as complete. Release claims should use the status
above until the corresponding evidence is added.
