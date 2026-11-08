# xapi-go

XML codec profiles: XPLATFORM 4000/5000 and Nexacro 4000. The conformance adapter also advertises JSON 1.0, SSV, and PlatformBinary 5000 profiles.

```go
type Response struct {
	ErrorCode int32 `xapi:"param=ErrorCode,type=INT"`
	Users []User `xapi:"dataset=users"`
}
type User struct {
	ID int32 `xapi:"col=id,type=INT"`
	Balance xapi.Decimal `xapi:"col=balance,type=BIGDECIMAL"`
}
err := xapi.Decode(r, &response, xapi.NexacroJSON10)
```

`Decimal`은 wire lexical value를 문자열로 보존하며 `[]byte` BLOB은 표준 base64를 사용합니다. Codec은 profile과 옵션을 생성 시 고정합니다.

기본 strict 모드에서는 BLOB base64 whitespace를 거부하며, legacy payload에는 `WithCompatibility()`를 사용할 수 있습니다.

`Codec`은 profile과 옵션을 보유하는 재사용 가능한 값입니다. 라이브러리는
전역 singleton을 만들지 않으며, 호출자가 애플리케이션 초기화 시 공통 Codec을
생성하고 공유하거나 profile/options별로 cache해야 합니다. 동일한 DTO type의
reflection schema는 내부적으로 cache됩니다.

```go
codec, err := xapi.NewCodec(
	xapi.NexacroJSON10,
	xapi.WithStrictMode(),
)
if err != nil {
	panic(err)
}

// 요청마다 NewCodec을 호출하지 않고 애플리케이션 수명 동안 재사용합니다.
if err := codec.Decode(r, &response); err != nil {
	// handle error
}
```

XML legacy 변형은 `WithXMLParameterValueAttribute()`와
`WithXMLVersionAttribute()`로 명시적으로 출력할 수 있으며, decoder는 두 Parameter
표현과 `ver`/`version` root attribute를 모두 읽습니다.

ConstColumn의 고정 wire 값은 `xapi:"const=market,type=STRING,value=KSE"`처럼
tag에 lexical value를 지정할 수 있습니다.

vendor-specific protocol 변형과 release readiness 상태는 compatibility 문서와 audit에 명시적으로 기록되어 있습니다.

현재 release readiness와 미충족 기준은 [docs/release-audit.md](docs/release-audit.md)에 evidence와 함께 기록되어 있습니다.
구현 phase별 범위와 검증 결과는 [docs/phase-report.md](docs/phase-report.md)에 정리되어 있습니다.
