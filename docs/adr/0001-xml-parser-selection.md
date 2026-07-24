# ADR 0001: XML parser

상태: accepted for initial implementation

초기 parser로 Go 표준 `encoding/xml.Decoder`를 선택한다. streaming token API, malformed input error, direct writer와 namespace 처리를 제공하고 외부 dependency를 추가하지 않는다.

## 후보와 관찰 결과

| 후보 | 결과 |
|---|---|
| Go `encoding/xml` | 선택. XML fixture, namespace, CDATA, malformed input, control-reference adapter를 통과 |
| 별도 third-party XML parser | 현재 도입하지 않음. 동일 fixture와 유지보수/license evidence를 repository에 고정할 때 재평가 |
| handwritten full parser | 거부. 현재 vocabulary에 필요한 entity/CDATA 처리는 reader adapter로 제한했으며 full parser의 유지보수 비용과 malformed-input 위험이 더 큼 |

## 요구사항별 evidence

| 항목 | `encoding/xml` 초기 구현 | 후보 간 비교 |
|---|---|---|
| license / maintenance | Go 표준 library, Go toolchain과 함께 유지 | third-party 후보의 버전·maintenance 자료를 고정하지 않아 미측정 |
| streaming events / direct writer | `xml.Decoder.Token`, `xml.Encoder`, 관련 conformance test | 동일 fixture의 대체 parser 결과 미측정 |
| malformed input / panic | malformed XML 오류와 XML fuzz smoke | 대체 parser fuzz 비교 미측정 |
| CDATA / entity / namespace | `codec_test.go`, public XML fixture, namespace test | 대체 parser conformance 미측정 |
| MB/s / rows/s / allocations / bytes | `BenchmarkXMLTokenize100kRows` parser-only benchmark와 `docs/performance.md`의 XML DTO/BLOB smoke, `encoding/xml.Unmarshal` baseline; 100k XML decode peak RSS `101,990,400 B` | third-party parser-only 후보별 비교 미측정 |
| control-character extension | `controlEntityReader`/`controlEntityWriter`, decimal·hex·CDATA test | 후보별 extension 비교 미측정 |

따라서 이 ADR은 초기 선택의 근거이지 parser 후보 간 성능 우열을 주장하는
보고서가 아니다. 대체 parser를 도입하려면 동일 fixture, malformed/fuzz,
CDATA/entity, MB/s, rows/s, allocations/op, bytes/op, peak memory를 같은
벤치마크로 추가 측정해야 한다.

현재 smoke benchmark에서 XML parser-only tokenization은 `85,456,875 ns/op`, `70.08 MB/s`, `75,203,232 B/op`, `2,600,073 allocs/op`이고, 100k XML DTO decode는 `131,963,916 ns/op`, `45.38 MB/s`, `117,096,712 B/op`, `3,301,042 allocs/op`, peak RSS는 `101,990,400 B`였다. 32MB XML BLOB encode는 `12,370,166 ns/op`, `2,712.53 MB/s`, `13,040 B/op`, `50 allocs/op`이었다. 측정 명령은 `/usr/bin/time -l env GOCACHE=/private/tmp/xapi-go-cache go test -run '^$' -bench '^BenchmarkXMLDecode100kRows$' -benchtime=1x -count=1 .`이며, 이는 parser 후보 간 정식 비교가 아니라 선택한 구현의 baseline이다.

XAPI ASCII control-character numeric reference는 일반 XML 1.0 입력과 다른
compatibility extension이다. `controlEntityReader`/`controlEntityWriter`가
표준 parser 앞뒤에서 이 vocabulary만 변환하고, 일반 XML parser를 대체하지
않는다. custom parser와 unsafe 최적화는 현재 범위에 넣지 않는다.
