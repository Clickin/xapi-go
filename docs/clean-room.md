# Clean-room 구현 고지

이 저장소는 공개된 XPLATFORM/Nexacro 문서와 독자적으로 작성한 fixture를 기준으로 protocol semantics를 구현한다. 상용 vendor adapter의 source, binary decompile, 내부 API, class 이름은 사용하지 않는다.

공개 문서로 확인되지 않은 변형은 구현하지 않고 `docs/protocol-compatibility.md`에 미확정으로 기록한다. 참조 구현은 wire shape와 상호운용성 관찰을 위한 조사 대상으로만 취급한다.

조사한 공개 참조 revision은 `Clickin/xapi-js` `244339098838d098d7087588d039a24d26448b5e`와
`Clickin/xplatform-xml` `cda8b6b31f64511ff9d22f64539c4b78e862455d`이다. 이 revision의
공개 타입·fixture·문서만 비교했으며 source, generated code, vendor binary 또는 내부
adapter를 복사하지 않았다. 확인된 차이는 본문 Parameter와 `value` attribute처럼
독립 fixture로 재현 가능한 wire variation만 구현했다.
