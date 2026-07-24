# Go XAPI Dataset Protocol Runtime — Agent Master Prompt

## 1. 프로젝트 목표

TOBEsoft XPLATFORM 및 Nexacro에서 사용하는 Dataset 통신 형식을 Go에서 타입 안전하게 처리하는 독립 라이브러리를 개발하라.

지원 형식:

- XPLATFORM Dataset XML 4000
- Nexacro Dataset XML 4000
- Nexacro Dataset JSON 1.0

개발자는 DTO를 직접 Go struct로 작성하고, struct tag 또는 선택적인 typed schema builder를 사용해 wire mapping을 명시한다.

핵심 목표:

1. 업무 코드가 구체적인 Go DTO만 사용하게 한다.
2. `Dataset.GetColumn(row, "column")`, `map[string]any`, 반복적인 type assertion을 일반 사용 경로에서 제거한다.
3. XML과 JSON이 동일한 DTO schema를 공유하게 한다.
4. lexical value와 decimal precision을 보존한다.
5. 대량 Dataset과 BLOB에서도 streaming으로 동작한다.
6. 라이브러리가 애플리케이션의 처리량 병목이 되지 않게 한다.
7. XPLATFORM/Nexacro legacy 변형을 명시적인 profile로 관리한다.
8. 공개 문서와 독자적인 검증 fixture에 기반한 clean-room 호환 구현으로 만든다.

이 프로젝트는 독립 Go repository이자 하나의 Go module로 유지한다.

- 다른 언어 source를 포함하지 않는다.
- 플랫폼 중립 IDL이나 generator를 필수 dependency로 두지 않는다.
- IDL 없이도 이 라이브러리가 완전히 동작해야 한다.

---

## 2. 참조 자료

조사 대상:

- TypeScript 구현: `https://github.com/Clickin/xapi-js`
- Java DTO binding 구현: `https://github.com/Clickin/xplatform-xml`
- TOBEsoft XPLATFORM/Nexacro 공개 문서
- 실제 또는 비식별화 wire fixture

참조 구현은 다음을 파악하기 위한 자료다.

- protocol semantics
- typed DTO binding
- lexical value 보존
- XML/JSON profile 차이
- RowType 및 OrgRow
- BLOB 처리
- ASCII 제어문자 entity 처리
- 성능 최적화
- 기존 시행착오

금지:

- TOBEsoft 상용 Java/C# adapter 코드 또는 내부 API 복제
- vendor binary decompile
- vendor package/class 이름 복제
- 공개 문서나 fixture로 확인되지 않은 동작의 추측 구현
- 기존 Java/TypeScript 구현의 버그까지 그대로 재현

다음 문서를 작성하라.

```text
docs/clean-room.md
docs/protocol-compatibility.md
docs/performance.md
docs/adr/
```

---

## 3. 사용자 경험

기본 방식은 handwritten DTO와 struct tag다.

```go
type SearchUsersResponse struct {
    ErrorCode int32     `xapi:"param=ErrorCode,type=INT"`
    ErrorMsg  string    `xapi:"param=ErrorMsg,type=STRING"`
    Users     []UserRow `xapi:"dataset=users"`
}

type UserRow struct {
    ID        int32         `xapi:"col=id,type=INT,size=10"`
    Name      string        `xapi:"col=name,type=STRING,size=100"`
    Balance   xapi.Decimal  `xapi:"col=balance,type=BIGDECIMAL,size=31"`
    CreatedAt xapi.DateTime `xapi:"col=createdAt,type=DATETIME"`
    Photo     []byte        `xapi:"col=photo,type=BLOB,optional"`
}
```

Decode:

```go
var response SearchUsersResponse

err := xapi.Decode(
    reader,
    &response,
    xapi.NexacroJSON10,
)
if err != nil {
    return err
}
```

Encode:

```go
err := xapi.Encode(
    writer,
    response,
    xapi.XPlatformXML4000,
)
```

편의 API:

```go
data, err := xapi.Marshal(response, xapi.NexacroXML4000)
err = xapi.Unmarshal(data, &response, xapi.NexacroJSON10)
```

일반 문서에서는 다음 사용을 권장하지 않는다.

```go
dataset.GetColumn(0, "name")
map[string]any
value.(string)
```

저수준 dynamic API가 필요하면 별도 advanced package 또는 namespace로 격리한다.

---

## 4. 지원 protocol model

완전하게 모델링할 대상:

- Root
- Parameters / Parameter
- Datasets / Dataset
- ColumnInfo
- Column
- ConstColumn
- Rows / Row
- RowType
- OrgRow
- Nexacro JSON original row
- missing value
- empty value
- explicit JSON null
- XML entity
- XAPI ASCII control-character entity
- BLOB
- profile별 namespace와 version
- Content-Type과 charset

기본 wire type:

```text
STRING
INT
LONG
FLOAT
DOUBLE
DECIMAL
BIGDECIMAL
DATE
TIME
DATETIME
BLOB
BOOLEAN
```

XPLATFORM과 Nexacro가 모든 type을 동일하게 지원한다고 가정하지 않는다. Profile별 실제 지원 범위를 fixture와 compatibility matrix에 기록한다.

---

## 5. 필수 설계 원칙

### 5.1 Lexical value를 먼저 보존한다

XML `ColumnInfo.type` 또는 JSON metadata에 따라 값을 먼저 Go primitive로 변환하지 않는다.

```text
XML text / JSON lexical token
            ↓
lexical wire value
            ↓
DTO target Go type conversion
```

예:

```xml
<Column id="amount" type="DECIMAL"/>
<Col id="amount">65.7700</Col>
```

DTO가 `string`이면 `"65.7700"`을 유지한다. DTO가 exact decimal이면 정확히 변환한다. 중간에 `float64`를 거치지 않는다.

`ColumnInfo`는 다음 용도로 사용한다.

- wire contract 검증
- serialization metadata
- strict mode mismatch 검출
- BLOB/DATE/DECIMAL wire encoding 결정

Java 구현에서 얻은 핵심 교훈:

> Deserialize는 wire metadata가 아니라 DTO target type을 기준으로 수행한다.

### 5.2 Parameter, ConstColumn, Col은 동일한 scalar codec을 사용한다

다음 위치마다 별도 변환 코드를 만들지 않는다.

- Parameter
- ConstColumn
- Row Col
- OrgRow Col
- JSON row property

공통 scalar 경로로 다음 문제를 방지한다.

- 일반 BLOB은 base64지만 ConstColumn BLOB은 문자열화되는 문제
- Parameter DATE와 Dataset DATE의 형식 불일치
- Parameter DECIMAL만 `float64`를 거치는 문제
- BOOLEAN 표현이 위치마다 달라지는 문제
- empty/missing 처리 방식이 위치마다 달라지는 문제

모든 wire type에 대해 Parameter, ConstColumn, Col 각각의 round-trip test를 작성한다.

### 5.3 DECIMAL/BIGDECIMAL을 float64로 암묵 변환하지 않는다

Core가 특정 decimal library를 필수 dependency로 강제하지 않게 설계한다.

기본 제공:

- lexical string-backed `xapi.Decimal`
- `encoding.TextMarshaler`
- `encoding.TextUnmarshaler`
- 사용자 scalar codec 등록
- 선택적 third-party decimal integration

`BIGDECIMAL`과 `float64`를 연결하는 경우 strict mode에서 오류로 처리하거나 명시적인 precision-loss opt-in이 있어야 한다.

조용한 precision loss는 허용하지 않는다.

### 5.4 BLOB은 항상 base64 wire representation이다

기본 DTO 타입은 `[]byte`다.

```go
type AttachmentRow struct {
    Content []byte `xapi:"col=content,type=BLOB"`
}
```

지원:

- XML BLOB text base64 decode
- JSON BLOB string base64 decode
- 표준 base64 encode
- zero-length BLOB
- invalid alphabet
- invalid padding
- whitespace strict/compatibility policy
- 최대 decode 크기
- Parameter BLOB
- ConstColumn BLOB
- Row BLOB
- OrgRow BLOB

가능하면 streaming API를 사용한다.

```go
base64.NewDecoder(...)
base64.NewEncoder(...)
```

대형 BLOB에서 다음을 동시에 보유하지 않는다.

```text
원본 []byte
+ 완성된 base64 string
+ 완성된 XML/JSON document
```

목표:

```text
BLOB bytes
 → base64 encoder
 → 최종 XML/JSON writer
```

Decode:

```text
XML/JSON scalar stream
 → base64 decoder
 → destination
```

선택적 advanced abstraction:

```go
type BlobSource interface {
    Open() (io.ReadCloser, error)
    Size() int64
}
```

필수 BLOB fixture:

- 0/1/2/3 byte
- padding 경계
- 1MB
- 32MB
- invalid character
- invalid padding
- whitespace
- oversized BLOB
- Parameter
- ConstColumn
- Row
- OrgRow

### 5.5 XAPI ASCII 제어문자 entity를 지원한다

Compatibility profile에서 XML 1.0 text에 직접 쓸 수 없는 ASCII 제어문자를 decimal numeric character reference로 처리한다.

Encode 대상:

```text
0x01–0x08
0x0B–0x0C
0x0E–0x1F
```

일반 whitespace로 유지:

```text
0x09 TAB
0x0A LF
0x0D CR
0x20 SPACE
```

NUL `0x00`은 오류다.

예:

```text
0x01 → &#1;
0x1F → &#31;
```

Decode는 numeric reference를 정확히 한 번만 복원한다.

`&amp;#1;`은 literal `"&#1;"`로 해석되어야 하며 다시 control character로 double-decode되어서는 안 된다.

반복적인 `strings.ReplaceAll` 순서에 의존하지 않는다. XML parser event 또는 single-pass decoder를 사용한다.

테스트:

- `&`
- `&amp;`
- literal `&#1;`
- 실제 0x01
- `&amp;#1;`
- malformed entity
- hexadecimal numeric reference
- CDATA
- attribute
- 연속된 제어문자
- 한글 혼합
- NUL

이 기능은 일반 XML 기능이 아니라 XAPI compatibility extension임을 문서화한다.

### 5.6 Missing, empty, null을 구분한다

다음 상태를 합치지 않는다.

- XML Col 없음
- XML self-closing Col
- XML empty Col
- JSON property 없음
- JSON explicit null
- 빈 문자열
- 0
- false
- empty BLOB

Generic optional type을 지원한다.

```go
type Optional[T any] struct {
    Present bool
    Null    bool
    Value   T
}
```

실제 fixture상 explicit JSON null 구분이 불필요하면 public API를 단순화할 수 있으나 내부 wire 상태는 손실하지 않는다.

XML writer 의미:

```xml
<!-- missing -->
Col 자체를 출력하지 않음

<!-- empty string -->
<Col id="name"></Col>
```

Missing과 empty를 모두 self-closing element로 출력하는 문제를 반복하지 않는다.

### 5.7 Column 순서에 의존하지 않는다

`ColumnInfo`와 Row의 `<Col>` 순서가 같다고 가정하지 않는다.

금지:

```text
ColumnInfo index N → row.cols[N]
```

Dataset 시작 시 한 번 생성:

```text
wire column ID → compiled DTO field slot
```

Row마다 하지 말아야 할 작업:

- reflection metadata 재탐색
- 새 map 생성
- column ID linear search
- struct tag 재파싱

Unknown column policy:

- strict: error
- compatibility: ignore
- advanced: capture

### 5.8 Empty Dataset에도 ColumnInfo를 출력한다

Row가 0개여도 DTO schema로 ColumnInfo를 출력한다.

```xml
<Dataset id="users">
  <ColumnInfo>
    <Column id="id" type="INT" size="10"/>
    <Column id="name" type="STRING" size="100"/>
  </ColumnInfo>
  <Rows/>
</Dataset>
```

첫 row에서 schema를 추론하지 않는다.

권장 inference 정책:

- string field는 STRING inference 검토 가능
- 그 외 wire type은 명시
- DECIMAL/BLOB/DATE 계열은 명시 필수
- 모호한 inference는 schema error

### 5.9 RowType과 OrgRow를 typed model로 노출한다

일반 Dataset:

```go
type SearchResponse struct {
    Users []UserRow `xapi:"dataset=users"`
}
```

Tracked Dataset:

```go
type SaveRequest struct {
    Users []xapi.Tracked[UserRow] `xapi:"dataset=users"`
}
```

공통 모델:

```go
type RowType uint8

const (
    RowNormal RowType = iota
    RowInsert
    RowUpdate
    RowDelete
)

type Tracked[T any] struct {
    Type     RowType
    Current  T
    Original *T
}
```

Mapping:

```text
XML: update Row + OrgRow
JSON: U row + 바로 다음 O row
```

검증:

- update가 아닌 row의 OrgRow
- original 없는 update
- 중복 OrgRow
- 첫 JSON row가 O
- U가 아닌 row 다음 O
- 연속 O
- delete original policy
- normal row original

### 5.10 날짜/시간은 timezone 없는 lexical type이다

무조건 `time.Time`으로 해석하지 않는다.

```go
type Date struct {
    // YYYYMMDD
}

type Time struct {
    // HHMMSS + fixture로 확인된 precision
}

type DateTime struct {
    // timezone 없는 local datetime
}
```

요구:

- timezone shift 없음
- exact lexical round-trip
- invalid date 검증
- leap year
- fractional precision
- unsupported precision error
- custom scalar 지원

기존 TypeScript 날짜 parser의 길이 가정을 검증 없이 복사하지 않는다.

### 5.11 Global mutable 설정을 금지한다

금지:

```go
var options Options

func Init(options Options) {
    // process-global mutation
}
```

불변 Codec:

```go
xmlCodec := xapi.NewCodec(
    xapi.XPlatformXML4000,
    xapi.WithStrictMode(),
)

jsonCodec := xapi.NewCodec(
    xapi.NexacroJSON10,
)
```

Codec은 concurrent use 가능하도록 설계하고 race test를 작성한다.

### 5.12 Content-Type parameter를 처리한다

지원:

```text
application/xml
application/xml; charset=UTF-8
text/xml
text/xml; charset=utf-8
application/json
application/json; charset=UTF-8
```

완전 일치 문자열 비교를 사용하지 않는다.

```go
mime.ParseMediaType(...)
```

를 사용한다.

XML declaration encoding과 실제 bytes가 다르면 오류 또는 명시적 transcoder를 적용한다. 모든 charset을 UTF-8로 조용히 읽지 않는다.

### 5.13 End-to-end streaming은 필수다

필수 API:

```go
func Decode(
    r io.Reader,
    dst any,
    profile Profile,
    options ...Option,
) error

func Encode(
    w io.Writer,
    src any,
    profile Profile,
    options ...Option,
) error
```

편의 API:

```go
func Marshal(src any, profile Profile, options ...Option) ([]byte, error)
func Unmarshal(data []byte, dst any, profile Profile, options ...Option) error
```

`Marshal`/`Unmarshal`은 streaming core를 재사용한다.

기본 구현에서 금지:

- 전체 HTTP body를 string으로 변환
- XML 전체 DOM 생성
- JSON 전체를 `map[string]any`로 변환
- dynamic Dataset 전체 생성 후 DTO 복사
- 모든 Reader에 `io.ReadAll`
- Writer API 내부 전체 output buffer
- BLOB encoded/decoded double buffering
- row마다 reflection

Decode:

```text
io.Reader
 → XML/JSON event parser
 → compiled field mapping
 → DTO assignment
```

Encode:

```text
DTO
 → compiled schema
 → XML/JSON event writer
 → io.Writer
```

### 5.14 XML library 선택은 자유지만 검증한다

초기에는 `encoding/xml`을 사용할 수 있다. 다음 요구를 만족하지 못하면 다른 XML library 도입을 허용한다.

- streaming events
- CDATA
- entity 제어
- namespace
- malformed input error
- allocation
- throughput
- control-character compatibility
- direct writer output

Parser ADR:

```text
docs/adr/xxxx-xml-parser-selection.md
```

포함:

- 후보
- license
- maintenance
- conformance 결과
- malformed fixture
- MB/s
- rows/s
- allocations/op
- bytes/op
- peak memory
- CDATA/entity
- control-character extension
- 선택/탈락 이유

Custom parser는 최후 수단이다.

조건:

- 기존 library의 구체적 한계
- XAPI vocabulary로 scope 제한
- fuzzing
- malformed input panic 없음
- entity/CDATA tests
- benchmark상 명확한 이득
- 유지보수 가능한 state machine

### 5.15 성능은 first-class requirement다

이 라이브러리가 애플리케이션 병목이 되어서는 안 된다.

측정:

- MB/s
- rows/s
- allocations/op
- bytes allocated/op
- peak heap/RSS
- schema compile
- cached schema
- BLOB throughput
- XML/JSON encode/decode

Release blocker:

- 전체 document 이중 materialization
- row마다 reflection/map
- BLOB double buffering
- 모든 scalar의 불필요한 allocation
- 100k rows에서 DOM과 유사한 peak memory
- 입력 크기에 비선형적인 memory
- malformed input panic
- infinite loop
- cancellation 이후 계속되는 처리

근거 없이 금지:

- unsafe
- reflection internals
- custom allocator
- 복잡한 object pool
- architecture-specific code
- handwritten parser
- assembly

### 5.16 Path-aware error를 제공한다

예:

```text
SearchUsersResponse.Users[17].Balance:
cannot decode "12A.30" as BIGDECIMAL
```

포함 가능한 정보:

- root DTO
- Dataset ID
- row index
- Column/Parameter ID
- wire type
- target Go type
- 제한된 lexical preview
- source error

BLOB 전체나 민감정보를 오류에 포함하지 않는다.

### 5.17 Security limits를 기본 제공한다

```go
type Limits struct {
    MaxDocumentBytes int64
    MaxDepth         int
    MaxDatasets      int
    MaxColumns       int
    MaxRows          int
    MaxScalarBytes   int64
    MaxBlobBytes     int64
}
```

XML:

- DTD 거부
- external entity 거부
- depth
- malformed entity
- entity expansion 방지
- duplicate attribute policy

JSON:

- depth
- number length
- string length
- row count
- duplicate key policy

---

## 6. Profile 설계

```go
type Profile uint8

const (
    XPlatformXML4000 Profile = iota
    NexacroXML4000
    NexacroJSON10
)
```

Profile이 포함할 정책:

- format
- namespace
- version
- version attribute
- Parameter representation
- Dataset wrapper
- RowType representation
- original row representation
- null policy
- date/time
- BLOB
- Content-Type
- compatibility variations

Profile과 options는 immutable해야 한다.

---

## 7. DTO schema

지원 tag:

```text
param=<id>
dataset=<id>
col=<id>
const=<id>
type=<wire-type>
size=<number>
optional
rowtype
orgrow
```

Schema compile 검증:

- 빈 ID
- 중복 Parameter/Dataset/Column
- ConstColumn collision
- invalid wire type
- invalid size
- conflicting tag
- Dataset field가 slice가 아님
- row type이 struct가 아님
- OrgRow type mismatch
- RowType field mismatch
- wire type과 Go type mismatch
- unsupported pointer nesting
- unexported field
- invalid recursive structure
- BLOB과 비-byte type
- DECIMAL과 float strict incompatibility

Schema는 `reflect.Type`별로 한 번 compile하고 immutable cache에 둔다.

### 선택적 typed schema builder

```go
var UserRowSchema = xapi.RowSchemaOf[UserRow](
    xapi.Column(
        "id",
        xapi.Int,
        func(row *UserRow) *int32 { return &row.ID },
    ),
    xapi.Column(
        "name",
        xapi.String(100),
        func(row *UserRow) *string { return &row.Name },
    ),
)
```

이 builder는 generator가 아니다. Struct tag와 builder는 동일한 compiled schema와 codec을 사용한다.

---

## 8. 권장 repository 구조

```text
/
├─ codec.go
├─ profile.go
├─ schema.go
├─ schema_tag.go
├─ schema_builder.go
├─ scalar.go
├─ decimal.go
├─ datetime.go
├─ blob.go
├─ row.go
├─ presence.go
├─ errors.go
├─ limits.go
├─ xml_reader.go
├─ xml_writer.go
├─ json_reader.go
├─ json_writer.go
├─ dynamic/
├─ xapihttp/
├─ internal/
├─ testdata/
│  ├─ xplatform-xml/
│  ├─ nexacro-xml/
│  ├─ nexacro-json/
│  ├─ invalid/
│  └─ expected/
├─ docs/
│  ├─ protocol-compatibility.md
│  ├─ clean-room.md
│  ├─ performance.md
│  └─ adr/
├─ go.mod
└─ README.md
```

불필요하게 package를 잘게 나누지 않는다.

---

## 9. 구현 단계

### Phase G1 — 조사와 skeleton

조사:

- xapi-js wire model/parser/writer/schema/adapters
- Java annotation processor/plan
- Java lexical-preservation 변경
- Java Content-Type/framework 시행착오
- 공식 XML/JSON 문서
- 실제 fixture

작성:

```text
docs/protocol-compatibility.md
docs/clean-room.md
```

Compatibility matrix:

- namespace
- root version
- `ver`/`version`
- Dataset wrapper
- Parameter text/value
- ColumnInfo
- ConstColumn
- XML RowType
- OrgRow
- JSON shape
- JSON RowType/O row
- null/missing/empty
- date/time
- BLOB
- charset
- Content-Type

미확정 내용은 명시한다.

완료:

- Go module
- API skeleton
- profile skeleton
- compile tests
- `go test ./...`

### Phase G2 — schema/scalar core

구현:

- tag parser
- schema compiler/cache
- validation
- scalar codec
- LexicalValue
- Decimal
- Date/Time/DateTime
- BLOB
- Optional/Presence
- Limits
- RowType/Tracked
- path-aware error

완료:

- invalid tags
- overflow
- exact decimal
- date validation
- base64
- missing/null/empty
- `go test -race ./...`

### Phase G3 — XML codec

Streaming reader/writer.

지원:

- XPLATFORM/Nexacro XML
- direct Dataset
- 검증된 wrapper
- Parameter text/value
- ColumnInfo/ConstColumn
- empty Dataset
- reordered/sparse Col
- RowType/OrgRow
- CDATA/entities
- control entity
- NUL rejection
- BLOB
- date/time
- Unicode
- limits

완료:

- round-trip
- canonical snapshots
- malformed fixtures
- control fixtures
- BLOB fixtures
- 10k/100k benchmarks
- panic 없음

### Phase G4 — Nexacro JSON codec

공식 fixture로 shape를 확정한다.

기본 구현에서 전체 `map[string]any` materialization을 금지한다.

`json.Decoder`, token API, `UseNumber` 또는 동등한 lexical-preserving 방식을 사용한다.

지원:

- Parameters
- Datasets
- ColumnInfo
- Rows
- N/I/U/D/O
- streaming U/O reconstruction
- missing/null/empty
- exact decimal
- BLOB
- date/time
- reordered properties
- duplicate key policy

O row 때문에 Dataset 전체를 보관하지 않는다.

완료:

- JSON round-trip
- XML과 동일 DTO
- exact decimal
- U/O
- BLOB
- 100k rows
- bounded memory

### Phase G5 — Public API

```go
func NewCodec(profile Profile, options ...Option) (*Codec, error)
func (c *Codec) Decode(r io.Reader, dst any) error
func (c *Codec) Encode(w io.Writer, src any) error
```

Top-level:

```text
Marshal
Unmarshal
Encode
Decode
Validate
MustValidate
```

Options:

```text
WithStrictMode
WithUnknownColumnPolicy
WithMissingDatasetPolicy
WithLimits
WithBigDecimalEncoding
WithJSONNullPolicy
WithCompatibility
```

Concurrent use를 테스트한다.

### Phase G6 — net/http integration

Core는 `net/http`에 의존하지 않는다.

`xapihttp`에 구현한다.

요구:

- streaming request encode
- streaming response decode
- `io.ReadAll` 기본 금지
- Content-Type parameter
- charset
- counting reader
- cancellation
- non-2xx
- typed decode error
- custom headers/auth
- body close
- malformed payload

Gin/Echo/Fiber는 초기 범위에서 제외한다.

### Phase G7 — Conformance/fuzz/benchmark

Fixture:

```text
testdata/
├─ xplatform-xml/
├─ nexacro-xml/
├─ nexacro-json/
├─ invalid/
└─ expected/
```

Compatibility:

- xapi-js
- Java 구현
- 실제 XPLATFORM/Nexacro
- Go 구현

Fuzz:

- XML parser
- entity/control decoder
- JSON reader
- tag parser
- scalar conversion
- base64
- U/O
- date/time

Benchmark:

- parameter-only
- 20 columns × 100/10k/100k rows
- sparse/dense
- 1MB/32MB BLOB
- Unicode/control
- OrgRow/UO
- parser only
- parser+DTO
- encode
- BLOB
- schema cold/cache
- allocations/memory

### Phase G8 — Parser 선택과 release

`encoding/xml`과 대체 parser를 비교할 수 있다.

모든 후보는 동일 conformance fixture를 먼저 통과해야 한다.

작성:

```text
docs/adr/xxxx-xml-parser-selection.md
docs/performance.md
```

CI:

```text
go test ./...
go test -race ./...
go vet ./...
gofmt check
fuzz smoke
license check
```

Stable release 전 검토:

- public API
- deprecated/experimental API
- compatibility matrix
- security limits
- minimum Go version
- license
- clean-room notice
- unsupported behavior
- documentation

---

## 10. Java/TypeScript 시행착오 체크리스트

- [ ] Wire type으로 먼저 coercion하지 않는가
- [ ] String DTO가 decimal lexical value를 보존하는가
- [ ] BIGDECIMAL이 float64를 거치지 않는가
- [ ] Parameter/ConstColumn/Col이 같은 scalar codec을 쓰는가
- [ ] ConstColumn BLOB이 base64인가
- [ ] Date/Time format이 위치별로 같은가
- [ ] ASCII control entity가 single-pass인가
- [ ] `&amp;#1;`을 double-decode하지 않는가
- [ ] NUL을 거부하는가
- [ ] missing과 empty를 구분하는가
- [ ] missing Dataset을 무조건 empty slice로 숨기지 않는 strict mode가 있는가
- [ ] ColumnInfo와 Col 순서를 동일하다고 가정하지 않는가
- [ ] empty Dataset에서 ColumnInfo를 출력하는가
- [ ] typed API에 RowType/OrgRow가 있는가
- [ ] global mutable option이 없는가
- [ ] Content-Type parameter를 처리하는가
- [ ] charset을 무조건 UTF-8로 읽지 않는가
- [ ] XML DOM을 만들지 않는가
- [ ] JSON 전체 map을 만들지 않는가
- [ ] BLOB double buffering이 없는가
- [ ] row마다 reflection하지 않는가
- [ ] malformed input에서 panic하지 않는가
- [ ] framework integration이 core와 분리되었는가

---

## 11. 최종 합격 기준

1. XML/JSON이 동일 handwritten DTO를 사용한다.
2. 일반 업무 코드에 string column lookup이 없다.
3. DECIMAL/BIGDECIMAL precision을 보존한다.
4. BLOB이 모든 위치에서 base64다.
5. ASCII control entity가 정확하다.
6. missing/null/empty를 구분한다.
7. Column 순서 독립적이다.
8. Empty Dataset에도 ColumnInfo가 있다.
9. RowType/OrgRow가 typed API에 있다.
10. XML/JSON이 streaming이다.
11. 대형 BLOB double buffering이 없다.
12. Codec이 immutable/concurrent-safe하다.
13. strict/compatibility mode가 분리된다.
14. security limits가 적용된다.
15. 100k rows benchmark/memory가 문서화된다.
16. parser ADR이 있다.
17. `go test -race ./...`가 통과한다.
18. clean-room/public API 문서가 준비된다.

---

## 12. Agent 작업 방식

- Repository를 먼저 읽는다.
- 기존 코드와 중복 구현하지 않는다.
- 각 phase를 작은 리뷰 단위로 유지한다.
- 미확정 protocol을 추측하지 않는다.
- fixture 없는 compatibility 예외를 추가하지 않는다.
- benchmark 없는 unsafe 최적화를 하지 않는다.
- 테스트를 삭제하거나 약화하지 않는다.
- public API 변경 영향과 migration을 보고한다.
- 불필요한 전체 재작성을 하지 않는다.
- 합리적인 기본 결정을 내려 진행한다.
- 불확실성은 문서화한다.

Phase 완료 보고:

```text
1. 구현 범위
2. 설계 결정
3. 주요 변경 파일
4. 테스트 결과
5. benchmark 결과
6. memory 결과
7. protocol 불확실성
8. 남은 작업
9. 다음 phase
```
