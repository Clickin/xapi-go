# Performance

Codec의 decode 경로는 `io.Reader` 기반 XML 토큰 처리와 bounded JSON lexer를 사용하고 encode 경로는 `io.Writer`를 사용한다. `Marshal`/`Unmarshal`은 편의 API이며 호출자가 전체 결과를 요청한 경우에만 buffer를 만든다.

현재 수치는 고정 benchmark fixture의 smoke 결과다. peak RSS는 process-level 측정까지 기록했지만, throughput release gate로 사용하려면 실행 환경과 parser 후보 비교 harness를 동일하게 고정해야 한다.

현재 smoke baseline (`Apple M4`, Go 1.26, 1,000-row JSON decode, 1 iteration): `1,538,583 ns/op`, `1,445,056 B/op`, `13,050 allocs/op`. 이 값은 안정적인 release target이 아니며, benchmark fixture와 실행 환경을 고정한 뒤 비교해야 한다.

추가 smoke 측정 (`Apple M4`, Go 1.26, 1 iteration): 100k-row JSON decode `69,505,709 ns/op`, `55.95 MB/s`, `144,704,088 B/op`, `1,300,069 allocs/op`; XML 100k-row decode `128,998,334 ns/op`, `46.43 MB/s`, `117,096,664 B/op`, `3,301,040 allocs/op`; JSON 100k-row encode는 기존 fixture 기준 `57,302,153 ns/op`, `24,043,642 B/op`, `2,300,157 allocs/op`이었다. row benchmark의 heap 수치는 DTO와 reflection allocation을 포함한다. BLOB encode 자체는 base64 encoded string을 만들지 않고 `base64.NewEncoder`로 writer에 보낸다.

동일 100k fixture의 one-iteration baseline은 XML `encoding/xml.Unmarshal`이
`142,073,208 ns/op`, `123,392,328 B/op`, `3,900,163 allocs/op`, JSON
`encoding/json.Unmarshal` into `any`가 `33,962,375 ns/op`,
`49,645,800 B/op`, `1,000,061 allocs/op`이었다. 이 baseline은 protocol DTO
conformance가 아닌 표준 library materialization 비교이며, peak RSS 비교는 아직
별도 측정하지 않았다.

JSON 32MB BLOB decode smoke: `191,207,750 ns/op`, `175.49 MB/s`, `67,298,000 B/op`, `75 allocs/op`; XML 32MB BLOB encode smoke: `12,370,166 ns/op`, `2,712.53 MB/s`, `13,040 B/op`, `50 allocs/op`이었다. JSON은 모든 schema에서 자체 bounded lexer를 사용하며, base64 BLOB은 decoded destination accumulator에 직접 연결한다. 따라서 입력 document와 완성된 base64 string을 별도로 materialize하지 않는다. 단, `Rows`가 `id`보다 먼저 오거나 property order를 해석할 수 없는 비정상 순서에서는 generic row fallback이 map과 값을 materialize한다.

개별 benchmark 프로세스의 peak RSS도 `/usr/bin/time -l ... go test -run '^$' -bench '^Benchmark...$' -benchtime=1x`로 측정했다. Go test 프로세스 기동 비용을 포함한 값이며, `Apple M4`, Go 1.26 기준으로 JSON 100k decode `104,513,536 B`, XML 100k decode `101,990,400 B`, JSON 32MB BLOB decode `168,148,992 B`, XML 32MB BLOB encode `102,105,088 B`였다. 이는 allocator 내부와 test binary 비용을 포함하므로 DTO peak heap의 대체값이 아니며, parser 후보 간 비교에서는 동일한 process harness를 사용해야 한다.
