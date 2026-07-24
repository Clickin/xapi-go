# HTTP integration

`xapihttp` is optional and owns the `net/http` dependency. `Client.Do` uses an `io.Pipe`: request encoding writes directly to the HTTP request body, and the response body is decoded directly from `http.Response.Body`. It closes both response body and request pipe, honors context cancellation through `NewRequestWithContext`, sets a parameterized Content-Type, and rejects non-2xx responses.

호출자는 `Client{HTTP: hc, Headers: http.Header{"Authorization": {"Bearer …"}}}`처럼
공통 헤더나 인증 헤더를 주입할 수 있다. request/response가 malformed이면 codec 오류를
반환하며 response body는 항상 닫는다.
