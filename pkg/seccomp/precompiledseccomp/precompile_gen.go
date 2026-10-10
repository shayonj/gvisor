// Copyright 2023 The gVisor Authors.
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

// precompile_gen generates a Go library that contains precompiled seccomp
// programs.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"gvisor.dev/gvisor/pkg/seccomp/precompiledseccomp"
	"gvisor.dev/gvisor/runsc/flag"

	// This import will be replaced by the one specified in the genrule,
	// or removed if stubbed out in fastbuild mode.
	"gvisor.dev/gvisor/pkg/seccomp/precompiledseccomp/example" // REPLACED_IMPORT_THIS_IS_A_LOAD_BEARING_COMMENT
)

// Flags.
var (
	output      = flag.String("out", "/dev/stdout", "output file")
	packageName = flag.String("package", "", "output package name")
)

// loadProgramsFn loads seccomp programs to be precompiled.
// It may be nil when it is stubbed out in fastbuild mode.
var loadProgramsFn = example.PrecompiledPrograms // PROGRAMS_FUNC_THIS_IS_A_LOAD_BEARING_COMMENT

func main() {
	flag.Parse()

	debug.SetGCPercent(1000)
	debug.SetMemoryLimit(2 << 30)

	var programs []precompiledseccomp.Program
	disabledAtBuildTime := loadProgramsFn == nil
	if !disabledAtBuildTime {
		var err error
		programs, err = loadProgramsFn()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cannot get list of programs to precompile: %v\n", err)
			os.Exit(1)
		}
	}
	if err := precompiledseccomp.WriteLibrary(*output, *packageName, programs, disabledAtBuildTime); err != nil {
		fmt.Fprintf(os.Stderr, "Cannot write precompiled programs to %q: %v\n", *output, err)
		os.Exit(1)
	}
}
