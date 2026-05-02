// Command predictor is the Go heuristic predictor service (the always-present fallback
// per ADR-0019). The Python LightGBM service lives in services/predictor-ml/ and shares
// the same gRPC contract.
// In E-01 it is a stub. Implementation arrives in E-05.
package main

import (
	"fmt"

	"github.com/teo-dev/teo/internal/version"
)

func main() {
	fmt.Println(version.Get("predictor"))
}
