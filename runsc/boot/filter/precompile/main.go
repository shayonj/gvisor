// Copyright 2026 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Binary precompile writes the sentry's precompiled seccomp-bpf programs to the
// filter_precompiled.go file of package filter, for the architecture it runs
// on. Bazel builds generate that file themselves. Builds from the go branch,
// which ships it without programs, can run this binary instead.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"gvisor.dev/gvisor/pkg/seccomp/precompiledseccomp"
	"gvisor.dev/gvisor/runsc/boot/filter/config"
	"gvisor.dev/gvisor/runsc/flag"
)

var output = flag.String("out", "runsc/boot/filter/filter_precompiled.go", "output file")

func main() {
	flag.Parse()

	debug.SetGCPercent(1000)
	debug.SetMemoryLimit(2 << 30)

	programs, err := config.PrecompiledPrograms()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot get list of programs to precompile: %v\n", err)
		os.Exit(1)
	}
	if err := precompiledseccomp.WriteLibrary(*output, "filter", programs, false); err != nil {
		fmt.Fprintf(os.Stderr, "Cannot write precompiled programs to %q: %v\n", *output, err)
		os.Exit(1)
	}
}
