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
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func createTempCSV(directory string, data [][]string) (string, error) {
	csvFile, err := os.CreateTemp(directory, "test-*.csv")
	if err != nil {
		return "", fmt.Errorf("Unable to create temp file: %w", err)
	}

	csvWriter := csv.NewWriter(csvFile)
	defer csvWriter.Flush()

	for _, row := range data {
		if err := csvWriter.Write(row); err != nil {
			return "", fmt.Errorf("Unable to write to temp file: %w", err)
		}
	}

	return csvFile.Name(), nil
}

func readCSV(filePath string) ([][]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open CSV: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	return reader.ReadAll()
}

func resetCSV(filePath string, data [][]string) error {
	csvFile, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("Failed to reset CSV: %w", err)
	}
	defer csvFile.Close()

	writer := csv.NewWriter(csvFile)
	defer writer.Flush()

	for _, row := range data {
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("failed to write row to CSV: %w", err)
		}
	}
	return nil
}

func TestCleanHydratedDirectory(t *testing.T) {
	testCases := []struct {
		name              string
		filesToCreate     []string
		isGitkeepExpected bool
	}{
		{
			name:              "Basic",
			filesToCreate:     []string{"file1.yaml", "file2.yaml", ".gitkeep"},
			isGitkeepExpected: true,
		},
		{
			name:              "No .gitkeep",
			filesToCreate:     []string{"file1.yaml", "file2.yaml"},
			isGitkeepExpected: false,
		},
		{
			name:              "Files and subdirectories",
			filesToCreate:     []string{"file1.yaml", "file2.yaml", "subdir/file3.yaml", ".gitkeep"},
			isGitkeepExpected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempDir, err := os.MkdirTemp(os.TempDir(), "test-clean-hydrated")
			if err != nil {
				t.Fatalf("Failed to create temp directory: %v", err)
			}
			defer os.RemoveAll(tempDir)

			for _, f := range tc.filesToCreate {
				filePath := filepath.Join(tempDir, f)
				if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
					t.Fatal(err)
				}
				file, err := os.Create(filePath)
				if err != nil {
					t.Fatalf("Unable to create temp files: %v", err)
				}
				file.Close()
			}

			files, err := os.ReadDir(tempDir)
			if err != nil {
				t.Fatalf("Unable to get files in temp dir: %v", err)
			}

			if len(files) <= 1 {
				t.Fatalf("Test not setup correctly, expected more than one file before cleanHydratedDirectory() called")
			}

			err = cleanHydratedDirectory(tempDir)
			if err != nil {
				t.Fatalf("%v", err)
			}

			files, err = os.ReadDir(tempDir)
			if err != nil {
				t.Fatalf("Unable to get files in temp dir: %v", err)
			}

			if (tc.isGitkeepExpected && len(files) != 1) || (!tc.isGitkeepExpected && len(files) != 0) {
				t.Errorf("Expected all non-.gitkeep files to be deleted, found %v", files)
			}

		})
	}
}

func TestFindFieldIndices(t *testing.T) {
	testCases := []struct {
		name            string
		header          []string
		fields          []string
		expectedIndices map[string]int
		expectedError   error
	}{
		{
			name:            "Basic success",
			header:          []string{"cluster_name", "cluster_group", "cluster_tags"},
			fields:          []string{"cluster_name", "cluster_group", "cluster_tags"},
			expectedIndices: map[string]int{"cluster_name": 0, "cluster_group": 1, "cluster_tags": 2},
			expectedError:   nil,
		},
		{
			name:            "Retrieve one field",
			header:          []string{"cluster_name", "cluster_group", "cluster_tags"},
			fields:          []string{"cluster_group"},
			expectedIndices: map[string]int{"cluster_group": 1},
			expectedError:   nil,
		},
		{
			name:            "Missing field",
			header:          []string{"cluster_name", "cluster_group", "cluster_tags"},
			fields:          []string{"region"},
			expectedIndices: nil,
			expectedError:   &FieldsNotFoundError{Fields: []string{"region"}},
		},
		{
			name:            "Missing multiple fields",
			header:          []string{"cluster_name", "cluster_group", "cluster_tags"},
			fields:          []string{"region", "location", "province"},
			expectedIndices: nil,
			expectedError:   &FieldsNotFoundError{Fields: []string{"region", "location", "province"}},
		},
		{
			name:            "Nil Header",
			header:          nil,
			fields:          []string{"cluster_name", "cluster_group"},
			expectedIndices: nil,
			expectedError:   &FieldsNotFoundError{Fields: []string{"cluster_name", "cluster_group"}},
		},
		{
			name:            "Empty Header",
			header:          []string{},
			fields:          []string{"cluster_name", "cluster_tags"},
			expectedIndices: nil,
			expectedError:   &FieldsNotFoundError{Fields: []string{"cluster_name", "cluster_tags"}},
		},
		{
			name:            "Nil Fields",
			header:          []string{"cluster_name", "cluster_group", "cluster_tags"},
			fields:          nil,
			expectedIndices: map[string]int{},
			expectedError:   nil,
		},
		{
			name:            "Empty Fields",
			header:          []string{"cluster_name", "cluster_group", "cluster_tags"},
			fields:          []string{},
			expectedIndices: map[string]int{},
			expectedError:   nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			indices, err := findFieldIndices(tc.header, tc.fields...)

			if tc.expectedError != nil {
				if err == nil {
					t.Fatal("Expected an error, but got none")
				}
				if err.Error() != tc.expectedError.Error() {
					t.Errorf("Error mismatch:\nExpected: %v\nGot:      %v", tc.expectedError, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}
				if len(indices) != len(tc.expectedIndices) {
					t.Errorf("Indices length mismatch:\nExpected: %d\nGot: %d", len(tc.expectedIndices), len(indices))
				} else {
					for field, expectedIndex := range tc.expectedIndices {
						if index, ok := indices[field]; !ok || index != expectedIndex {
							t.Errorf("Index mismatch for field %q:\nExpected: %d\nGot: %d", field, expectedIndex, index)
						}
					}
				}
			}
		})
	}
}

func TestDetermineClustersToUpdate(t *testing.T) {
	testData := [][]string{
		{"cluster_name", "cluster_group", "cluster_tags"},
		{"cluster1", "groupA", "tag1,tag2"},
		{"cluster2", "groupB", "tag3,tag4"},
		{"cluster3", "groupA", "tag1,tag4,tag5"},
		{"cluster4", "groupA", "tag2"},
	}

	sourceOfTruth, err := createTempCSV("", testData)
	if err != nil {
		t.Fatalf("Failed to create test CSV: %v", err)
	}
	defer os.Remove(sourceOfTruth)

	testCases := []struct {
		name             string
		sourceOfTruth    string
		clusterGroup     string
		matchAnyTags     []string
		matchAllTags     []string
		expectedClusters []string
		expectedError    error
	}{
		{
			"Match any tags",
			sourceOfTruth,
			"groupA",
			[]string{"tag1"},
			[]string{},
			[]string{"cluster1", "cluster3"},
			nil,
		},
		{
			"Match all tags",
			sourceOfTruth,
			"groupA",
			[]string{},
			[]string{"tag1", "tag2"},
			[]string{"cluster1"},
			nil,
		},
		{
			"Invalid group",
			sourceOfTruth,
			"invalidGroup",
			[]string{"tag1"},
			[]string{},
			[]string{},
			nil,
		},
		{
			"Nonexistent File",
			"nonexistent_file.csv",
			"groupA",
			[]string{"tag1"},
			[]string{},
			[]string{},
			os.ErrNotExist,
		},
		{
			"No tags matches all clusters in group",
			sourceOfTruth,
			"groupA",
			[]string{},
			[]string{},
			[]string{"cluster1", "cluster3", "cluster4"},
			nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := determineClustersToUpdate("", tc.sourceOfTruth, tc.clusterGroup, tc.matchAnyTags, tc.matchAllTags)

			if tc.expectedError != nil {
				if err == nil {
					t.Fatal("Expected an error, but got none")
				}
				if !errors.Is(err, tc.expectedError) {
					t.Errorf("Error mismatch:\nExpected: %v\nGot:      %v", tc.expectedError, err.Error())
				}
			} else {
				if !slices.Equal(result, tc.expectedClusters) {
					t.Errorf("Expected clusters mismatch\nExpected: %v\n     Got: %v", tc.expectedClusters, result)
				}
			}
		})
	}
}

func TestUpdatePlatformAndWorkloadRepositoryRevision(t *testing.T) {
	testData := [][]string{
		{"cluster_name", "platform_repository_revision", "workload_repository_revision"},
		{"cluster1", "v0", "v0"},
		{"cluster2", "v0", "v0"},
		{"cluster3", "v0", "v0"},
	}

	sourceOfTruth, err := createTempCSV("", testData)
	if err != nil {
		t.Fatalf("Failed to create test CSV: %v", err)
	}
	defer os.Remove(sourceOfTruth)

	testCases := []struct {
		name             string
		clusterNames     []string
		sourceOfTruth    string
		platformRevision string
		workloadRevision string
		expectedData     [][]string
		expectedError    error
	}{
		{
			"Update platform revision",
			[]string{"cluster1"},
			sourceOfTruth,
			"v1",
			"",
			[][]string{
				{"cluster_name", "platform_repository_revision", "workload_repository_revision"},
				{"cluster1", "v1", "v0"},
				{"cluster2", "v0", "v0"},
				{"cluster3", "v0", "v0"},
			},
			nil,
		},
		{
			"Update workload revision",
			[]string{"cluster2"},
			sourceOfTruth,
			"",
			"v2",
			[][]string{
				{"cluster_name", "platform_repository_revision", "workload_repository_revision"},
				{"cluster1", "v0", "v0"},
				{"cluster2", "v0", "v2"},
				{"cluster3", "v0", "v0"},
			},
			nil,
		},
		{
			"Update both platform and workload revisions",
			[]string{"cluster1"},
			sourceOfTruth,
			"v3",
			"v4",
			[][]string{
				{"cluster_name", "platform_repository_revision", "workload_repository_revision"},
				{"cluster1", "v3", "v4"},
				{"cluster2", "v0", "v0"},
				{"cluster3", "v0", "v0"},
			},
			nil,
		},
		{
			"No updates",
			[]string{"cluster2"},
			sourceOfTruth,
			"",
			"",
			testData,
			nil,
		},
		{
			"Update multiple clusters at once",
			[]string{"cluster2", "cluster3"},
			sourceOfTruth,
			"v9",
			"v10",
			[][]string{
				{"cluster_name", "platform_repository_revision", "workload_repository_revision"},
				{"cluster1", "v0", "v0"},
				{"cluster2", "v9", "v10"},
				{"cluster3", "v9", "v10"},
			},
			nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resetCSV(sourceOfTruth, testData)

			err := updatePlatformAndWorkloadRepositoryRevision("", tc.clusterNames, tc.sourceOfTruth, tc.platformRevision, tc.workloadRevision)
			if tc.expectedError != nil {
				if err == nil {
					t.Fatal("Expected an error, but got none")
				}
				if err != nil && !errors.Is(err, tc.expectedError) {
					t.Errorf("Expected error: '%v', got: '%v'", tc.expectedError, err)
				}
			}

			records, err := readCSV(tc.sourceOfTruth)
			if err != nil {
				t.Fatalf("Failed to read CSV after update: %v", err)
			}
			if !reflect.DeepEqual(records, tc.expectedData) {
				t.Errorf("Expected output mismatch\nExpected: %s\n     Got: %s", tc.expectedData, records)
			}
		})
	}
}
