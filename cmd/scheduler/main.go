// Command scheduler hosts the LPT scheduler as a service for E2E tests and benchmarks.
// In E-01 it is a stub. Implementation arrives in E-05.
package main

import (
	"fmt"

	"github.com/teo-dev/teo/internal/version"
)

func main() {
	fmt.Println(version.Get("scheduler"))
}
