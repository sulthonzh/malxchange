// Command api is the member surface: auth, REST, the member WebSocket,
// and order submission (built out in issue #24; platform skeleton here).
package main

import "github.com/syaikhipin/malxchange/internal/service"

func main() { service.Main("api", nil) }
