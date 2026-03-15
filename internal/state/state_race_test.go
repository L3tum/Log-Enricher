package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitializeAndGetOrCreateFileState_NoDataRace(t *testing.T) {
	stateFilePath := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(stateFilePath, []byte(`{"files":{},"caches":{},"cache":{}}`), 0o644))
	require.NoError(t, Initialize(stateFilePath))

	const iterations = 2000
	const fileVariants = 8

	var wg sync.WaitGroup
	wg.Add(2)
	errCh := make(chan error, iterations)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if err := Initialize(stateFilePath); err != nil {
				errCh <- err
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			path := filepath.Join(filepath.Dir(stateFilePath), fmt.Sprintf("service-%d.log", i%fileVariants))
			_ = GetOrCreateFileState(path)
		}
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}
}

func TestSaveAndIncrementLineNumber_NoDataRace(t *testing.T) {
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "state.json")
	logPath := filepath.Join(tmpDir, "service.log")

	require.NoError(t, os.WriteFile(logPath, []byte("line\n"), 0o644))
	require.NoError(t, Initialize(stateFilePath))

	fileState := GetOrCreateFileState(logPath)

	const incrementIterations = 5000
	const saveIterations = 300

	errCh := make(chan error, saveIterations)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < incrementIterations; i++ {
			fileState.IncrementLineNumber()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < saveIterations; i++ {
			if err := Save(stateFilePath); err != nil {
				errCh <- err
				return
			}
		}
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}
}
