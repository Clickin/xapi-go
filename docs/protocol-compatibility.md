# Protocol compatibility

| 항목 | XPLATFORM XML 4000 | Nexacro XML 4000 | Nexacro JSON 1.0 |
|---|---|---|---|
| root | `Root xmlns="http://www.tobesoft.com/platform/Dataset" ver="4000"` | `Root xmlns="http://www.nexacroplatform.com/platform/dataset" ver/version="4000"` | object (`version: "1.0"`) |
| parameters | `Parameters/Parameter` (`id`, `type`, text 또는 `value` attribute) | 동일 shape | `Parameters` array (`id`, `type`, `value`) |
| dataset | `Dataset` (`id`) | 동일 shape | `Datasets` array (`id`, `ColumnInfo`, `Rows`) |
| schema | `ColumnInfo/Column`, `ConstColumn` | 동일 shape | `ColumnInfo: {Column: [...], ConstColumn: [...]}` |
| row values | `Rows/Row/Col` (`id`), `OrgRow` for update | 동일 shape | row object keyed by column id and `_RowType_` (`N/I/U/D/O`) |
| BLOB | base64 text | base64 text | base64 string |

Column 순서는 사용하지 않고 column ID로 매핑한다. 빈 dataset에도 DTO schema에서 `ColumnInfo`를 출력한다. Decimal은 float64를 거치지 않는다.

ConstColumn은 `xapi:"const=market,type=STRING,value=KSE"`처럼 선택적인
`value=<lexical>` tag로 wire constant를 지정할 수 있다. value가 없으면 기존
호환 동작대로 zero value를 사용한다.

XML decoder는 Parameter의 본문 text와 `value` attribute를 모두 허용한다. encoder의
기본 출력은 본문 text이며, 공개 구현과의 attribute-form interop가 필요하면
`WithXMLParameterValueAttribute()`를 명시한다. 이 모드에서도 BLOB Parameter는
대형 payload의 double buffering을 피하기 위해 본문 streaming을 유지한다.
Root version attribute는 기본적으로 `ver`를 쓰며, 공개 구현의 `version` spelling이
필요한 경우 `WithXMLVersionAttribute()`를 명시할 수 있다. decoder는 두 spelling을
모두 읽고 값은 `4000`인지 검증한다.

JSON Dataset object에서 `Rows`가 `id`보다 먼저 오는 경우 단일 compiled Dataset이면
typed streaming으로 처리한다. 여러 Dataset이 선언된 strict 입력은 대상이 모호하므로
오류로 처리하며, compatibility 입력만 legacy generic fallback을 허용한다.

Row 상태는 `Tracked[T]`를 기본 typed model로 사용할 수 있고, row struct에
`xapi:"rowtype"`와 `xapi:"orgrow"`를 표시하는 대안도 지원한다.

`DATE`는 strict 모드에서 `YYYYMMDD`, `TIME`은 6자리 또는 millisecond 9자리, `DATETIME`은 14자리 또는 millisecond 17자리 lexical form만 허용한다. `WithCompatibility()`에서는 legacy lexical value를 문자열로 보존한다.

공개 구현에서 관찰되는 type spelling alias `BIG_DECIMAL`, `DATE_TIME`, `BOOL`은
각각 canonical wire type `BIGDECIMAL`, `DATETIME`, `BOOLEAN`으로 정규화한다.

BLOB base64 whitespace는 기본 strict 경로에서 거부한다. legacy 입력을 읽을 때는
`WithCompatibility()` 또는 `WithBase64Whitespace(AllowBase64Whitespace)`를 명시해
space, tab, CR, LF를 허용할 수 있다. 이 정책은 Parameter, ConstColumn, Col,
OrgRow에 동일하게 적용된다.

다음은 공식 또는 비식별 fixture가 추가될 때 확정할 영역이다: explicit null 정책, 날짜·시간 fractional precision, charset transcoding, control-character numeric reference의 vendor별 허용 범위.

공개 Nexacro [XML Dataset Format](https://docs.tobesoft.com/advanced_development_guide_nexacro_n_v24_en/6cb28e9305b1177c)과 [JSON Dataset Format](https://docs.tobesoft.co.jp/developer_guide_nexacro_n_ja/54a26507883f3106)을 근거로 한다. XML의 `Root` namespace/version, `ConstColumn`, update `OrgRow`, JSON의 `version`, object형 `ColumnInfo`, `_RowType_`와 `U` 다음 `O` 규칙을 반영했다. XPLATFORM과 Nexacro의 나머지 vendor-specific 차이는 독립 fixture가 추가되기 전까지 동일하다고 추정하지 않는다.

공개 layout fixture는 `testdata/nexacro-xml/` 및 `testdata/nexacro-json/`에 보관하며, `conformance_test.go`가 동일 handwritten DTO로 decode와 lexical round-trip을 검증한다. 공개 참조 구현에서 확인한 Parameter attribute form은 `TestXMLParameterValueAttributeCompatibility`로 독립 검증한다.
