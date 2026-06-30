/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package support

import (
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// FindProjectRoot walks up from the current working directory to find the
// project root, identified by the presence of a go.mod file. This avoids
// hard-coded relative paths like "../../" which break when tests are run
// from different directories.
func FindProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found in any parent directory")
		}

		dir = parent
	}
}

// ProjectFile returns the absolute path to a file relative to the project root.
func ProjectFile(parts ...string) (string, error) {
	root, err := FindProjectRoot()
	if err != nil {
		return "", err
	}

	return filepath.Join(append([]string{root}, parts...)...), nil
}

// MustProjectFile is like ProjectFile but panics on error.
func MustProjectFile(parts ...string) string {
	p, err := ProjectFile(parts...)
	if err != nil {
		panic(err)
	}

	return p
}

// ReadConfigMapData reads a ConfigMap YAML file and returns its .data map.
// The file must contain a single ConfigMap resource.
func ReadConfigMapData(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", path, err)
	}

	var cm corev1.ConfigMap
	if err := yaml.Unmarshal(raw, &cm); err != nil {
		return nil, fmt.Errorf("parsing ConfigMap from %s: %w", path, err)
	}

	return cm.Data, nil
}

// MustReadConfigMapData is like ReadConfigMapData but panics on error.
func MustReadConfigMapData(path string) map[string]string {
	data, err := ReadConfigMapData(path)
	if err != nil {
		panic(err)
	}

	return data
}
