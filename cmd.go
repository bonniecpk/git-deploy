// Copyright 2023 Google LLC

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at

//     https://www.apache.org/licenses/LICENSE-2.0

// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// runCmd starts and waits for the provided command with args to complete. If the command
// succeeds it returns the stdout of the command.
func runCmd(binPath string, args []string, dir string, logCmd bool) ([]byte, error) {
	if logCmd {
		fmt.Printf("Running the following command: %s %s\n", binPath, args)
	}
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir

	var stderr bytes.Buffer
	errWriter := io.MultiWriter(&stderr, os.Stderr)
	cmd.Stderr = errWriter

	var stdout bytes.Buffer
	outWriter := io.MultiWriter(&stdout, os.Stdout)
	cmd.Stdout = outWriter

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start command: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("error running command: %v\n%s", err, stderr.Bytes())
	}
	return stdout.Bytes(), nil
}
