package infra

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// mockProcessManagerForFreedom implements domain.ProcessManager for testing
type mockProcessManagerForFreedom struct {
	findResults map[string][]int
	findErrors  map[string]error
	killCalled  []int
	killError   error
	runningPIDs map[int]bool
}

func newMockPMForFreedom() *mockProcessManagerForFreedom {
	return &mockProcessManagerForFreedom{
		findResults: make(map[string][]int),
		findErrors:  make(map[string]error),
		runningPIDs: make(map[int]bool),
	}
}

func (m *mockProcessManagerForFreedom) FindByName(pattern string) ([]int, error) {
	if err, ok := m.findErrors[pattern]; ok {
		return nil, err
	}
	if pids, ok := m.findResults[pattern]; ok {
		return pids, nil
	}
	return nil, nil
}

func (m *mockProcessManagerForFreedom) Kill(pid int) error {
	m.killCalled = append(m.killCalled, pid)
	return m.killError
}

func (m *mockProcessManagerForFreedom) IsRunning(pid int) bool {
	return m.runningPIDs[pid]
}

func (m *mockProcessManagerForFreedom) GetCurrentPID() int {
	return 99999
}

var _ domain.ProcessManager = (*mockProcessManagerForFreedom)(nil)

// mockCommandRunner implements CommandRunner for testing
type mockCommandRunner struct {
	runCalls    [][]string
	runError    error
	outputCalls [][]string
	outputMap   map[string][]byte
	outputError map[string]error
}

func newMockCommandRunner() *mockCommandRunner {
	return &mockCommandRunner{
		outputMap:   make(map[string][]byte),
		outputError: make(map[string]error),
	}
}

func (m *mockCommandRunner) Run(name string, args ...string) error {
	m.runCalls = append(m.runCalls, append([]string{name}, args...))
	return m.runError
}

func (m *mockCommandRunner) Output(name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	m.outputCalls = append(m.outputCalls, call)

	// Build key from command
	key := name
	if len(args) > 0 {
		key = name + " " + args[0]
	}

	if err, ok := m.outputError[key]; ok {
		return nil, err
	}
	if output, ok := m.outputMap[key]; ok {
		return output, nil
	}
	return nil, errors.New("command not found")
}

var _ CommandRunner = (*mockCommandRunner)(nil)

// mockFileChecker implements FileChecker for testing
type mockFileChecker struct {
	existsMap map[string]bool
}

func newMockFileChecker() *mockFileChecker {
	return &mockFileChecker{
		existsMap: make(map[string]bool),
	}
}

func (m *mockFileChecker) Exists(path string) bool {
	return m.existsMap[path]
}

var _ FileChecker = (*mockFileChecker)(nil)

// TestFreedomProtector_Constants verifies expected constant values
func TestFreedomProtector_Constants(t *testing.T) {
	assert.Equal(t, "/Applications/Freedom.app", FreedomAppPath)
	assert.Equal(t, "Freedom", FreedomProcessName)
	assert.Equal(t, "FreedomProxy", FreedomProxyProcessName)
	assert.Equal(t, "com.80pct.FreedomHelper", FreedomHelperProcessName)
	assert.Equal(t, 7769, FreedomProxyPort)
}

// TestNewFreedomProtector verifies constructor with default dependencies
func TestNewFreedomProtector(t *testing.T) {
	pm := newMockPMForFreedom()
	logger := zap.NewNop()

	protector := NewFreedomProtector(pm, logger)

	require.NotNil(t, protector)
	assert.Equal(t, pm, protector.pm)
	assert.Equal(t, logger, protector.logger)
	assert.NotNil(t, protector.cmdRunner)
	assert.NotNil(t, protector.fileChecker)
}

// TestNewFreedomProtectorWithDeps verifies constructor with custom dependencies
func TestNewFreedomProtectorWithDeps(t *testing.T) {
	pm := newMockPMForFreedom()
	logger := zap.NewNop()
	cmdRunner := newMockCommandRunner()
	fileChecker := newMockFileChecker()

	protector := NewFreedomProtectorWithDeps(pm, logger, cmdRunner, fileChecker)

	require.NotNil(t, protector)
	assert.Equal(t, pm, protector.pm)
	assert.Equal(t, logger, protector.logger)
	assert.Equal(t, cmdRunner, protector.cmdRunner)
	assert.Equal(t, fileChecker, protector.fileChecker)
}

// TestFreedomProtectorImpl_IsInstalled tests installation detection
func TestFreedomProtectorImpl_IsInstalled(t *testing.T) {
	tests := []struct {
		name     string
		exists   bool
		expected bool
	}{
		{"installed", true, true},
		{"not installed", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := newMockPMForFreedom()
			fileChecker := newMockFileChecker()
			fileChecker.existsMap[FreedomAppPath] = tt.exists
			protector := NewFreedomProtectorWithDeps(pm, zap.NewNop(), newMockCommandRunner(), fileChecker)

			result := protector.IsInstalled()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestFreedomProtectorImpl_IsAppRunning tests app running detection
func TestFreedomProtectorImpl_IsAppRunning(t *testing.T) {
	tests := []struct {
		name        string
		findResults map[string][]int
		findErrors  map[string]error
		psOutput    map[string][]byte
		psError     map[string]error
		expected    bool
	}{
		{
			name:        "no processes found",
			findResults: map[string][]int{},
			expected:    false,
		},
		{
			name: "Freedom process found and verified",
			findResults: map[string][]int{
				"Freedom": {12345},
			},
			psOutput: map[string][]byte{
				"ps -p": []byte("Freedom\n"),
			},
			expected: true,
		},
		{
			name: "FreedomProxy found (should not match)",
			findResults: map[string][]int{
				"Freedom": {12345},
			},
			psOutput: map[string][]byte{
				"ps -p": []byte("FreedomProxy\n"),
			},
			expected: false,
		},
		{
			name: "FreedomHelper found (should not match)",
			findResults: map[string][]int{
				"Freedom": {12345},
			},
			psOutput: map[string][]byte{
				"ps -p": []byte("FreedomHelper\n"),
			},
			expected: false,
		},
		{
			name: "ps command fails, fallback to FindByName",
			findResults: map[string][]int{
				"Freedom": {12345},
			},
			psError: map[string]error{
				"ps -p": errors.New("process not found"),
			},
			expected: true,
		},
		{
			name: "find error",
			findErrors: map[string]error{
				"Freedom": errors.New("process lookup failed"),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := newMockPMForFreedom()
			pm.findResults = tt.findResults
			pm.findErrors = tt.findErrors

			cmdRunner := newMockCommandRunner()
			if tt.psOutput != nil {
				cmdRunner.outputMap = tt.psOutput
			}
			if tt.psError != nil {
				cmdRunner.outputError = tt.psError
			}

			fileChecker := newMockFileChecker()
			protector := NewFreedomProtectorWithDeps(pm, zap.NewNop(), cmdRunner, fileChecker)

			result := protector.IsAppRunning()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestFreedomProtectorImpl_IsProxyRunning tests proxy running detection
func TestFreedomProtectorImpl_IsProxyRunning(t *testing.T) {
	tests := []struct {
		name        string
		findResults map[string][]int
		findErrors  map[string]error
		expected    bool
	}{
		{
			name:        "no proxy found",
			findResults: map[string][]int{},
			expected:    false,
		},
		{
			name: "FreedomProxy found",
			findResults: map[string][]int{
				"FreedomProxy": {54321},
			},
			expected: true,
		},
		{
			name: "multiple proxies found",
			findResults: map[string][]int{
				"FreedomProxy": {54321, 54322},
			},
			expected: true,
		},
		{
			name: "find error",
			findErrors: map[string]error{
				"FreedomProxy": errors.New("lookup failed"),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := newMockPMForFreedom()
			pm.findResults = tt.findResults
			pm.findErrors = tt.findErrors
			protector := NewFreedomProtectorWithDeps(pm, zap.NewNop(), newMockCommandRunner(), newMockFileChecker())

			result := protector.IsProxyRunning()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestFreedomProtectorImpl_IsHelperRunning tests helper running detection
func TestFreedomProtectorImpl_IsHelperRunning(t *testing.T) {
	tests := []struct {
		name        string
		findResults map[string][]int
		findErrors  map[string]error
		pgrepOutput []byte
		pgrepError  error
		expected    bool
	}{
		{
			name:        "helper found via FindByName",
			findResults: map[string][]int{"com.80pct.FreedomHelper": {815}},
			expected:    true,
		},
		{
			name:        "helper found via pgrep fallback",
			findResults: map[string][]int{},
			pgrepOutput: []byte("815\n"),
			expected:    true,
		},
		{
			name:        "helper not found anywhere",
			findResults: map[string][]int{},
			pgrepError:  errors.New("no process found"),
			expected:    false,
		},
		{
			name:        "FindByName errors, fallback to pgrep succeeds",
			findErrors:  map[string]error{"com.80pct.FreedomHelper": errors.New("lookup failed")},
			pgrepOutput: []byte("815\n"),
			expected:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := newMockPMForFreedom()
			pm.findResults = tt.findResults
			pm.findErrors = tt.findErrors

			cmdRunner := newMockCommandRunner()
			if tt.pgrepOutput != nil {
				cmdRunner.outputMap["pgrep -f"] = tt.pgrepOutput
			}
			if tt.pgrepError != nil {
				cmdRunner.outputError["pgrep -f"] = tt.pgrepError
			}

			protector := NewFreedomProtectorWithDeps(pm, zap.NewNop(), cmdRunner, newMockFileChecker())

			result := protector.IsHelperRunning()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestFreedomProtectorImpl_IsLoginItemPresent tests login item detection
func TestFreedomProtectorImpl_IsLoginItemPresent(t *testing.T) {
	tests := []struct {
		name           string
		osascriptOut   []byte
		osascriptError error
		expected       bool
	}{
		{
			name:         "Freedom in login items",
			osascriptOut: []byte("iTerm2, Freedom, Slack\n"),
			expected:     true,
		},
		{
			name:         "Freedom not in login items",
			osascriptOut: []byte("iTerm2, Slack\n"),
			expected:     false,
		},
		{
			name:         "empty login items",
			osascriptOut: []byte(""),
			expected:     false,
		},
		{
			name:           "osascript error",
			osascriptError: errors.New("system events not available"),
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmdRunner := newMockCommandRunner()
			if tt.osascriptOut != nil {
				cmdRunner.outputMap["osascript -e"] = tt.osascriptOut
			}
			if tt.osascriptError != nil {
				cmdRunner.outputError["osascript -e"] = tt.osascriptError
			}

			protector := NewFreedomProtectorWithDeps(newMockPMForFreedom(), zap.NewNop(), cmdRunner, newMockFileChecker())

			result := protector.IsLoginItemPresent()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestFreedomProtectorImpl_RestartApp tests app restart
func TestFreedomProtectorImpl_RestartApp(t *testing.T) {
	tests := []struct {
		name      string
		runError  error
		expectErr bool
	}{
		{
			name:      "successful restart",
			runError:  nil,
			expectErr: false,
		},
		{
			name:      "restart fails",
			runError:  errors.New("app not found"),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmdRunner := newMockCommandRunner()
			cmdRunner.runError = tt.runError

			protector := NewFreedomProtectorWithDeps(newMockPMForFreedom(), zap.NewNop(), cmdRunner, newMockFileChecker())

			err := protector.RestartApp()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Verify correct command was called
			require.Len(t, cmdRunner.runCalls, 1)
			assert.Equal(t, []string{"open", "-a", FreedomAppPath}, cmdRunner.runCalls[0])
		})
	}
}

// TestFreedomProtectorImpl_RestoreLoginItem tests login item restoration
func TestFreedomProtectorImpl_RestoreLoginItem(t *testing.T) {
	tests := []struct {
		name      string
		runError  error
		expectErr bool
	}{
		{
			name:      "successful restore",
			runError:  nil,
			expectErr: false,
		},
		{
			name:      "restore fails",
			runError:  errors.New("permission denied"),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmdRunner := newMockCommandRunner()
			cmdRunner.runError = tt.runError

			protector := NewFreedomProtectorWithDeps(newMockPMForFreedom(), zap.NewNop(), cmdRunner, newMockFileChecker())

			err := protector.RestoreLoginItem()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Verify osascript was called
			require.Len(t, cmdRunner.runCalls, 1)
			assert.Equal(t, "osascript", cmdRunner.runCalls[0][0])
		})
	}
}

// TestFreedomProtectorImpl_GetHealth tests health status aggregation
func TestFreedomProtectorImpl_GetHealth(t *testing.T) {
	pm := newMockPMForFreedom()
	pm.findResults = map[string][]int{
		"Freedom":      {100},
		"FreedomProxy": {200},
	}

	cmdRunner := newMockCommandRunner()
	cmdRunner.outputMap["ps -p"] = []byte("Freedom\n")
	cmdRunner.outputMap["pgrep -f"] = []byte("815\n")
	cmdRunner.outputMap["osascript -e"] = []byte("Freedom, Slack\n")

	fileChecker := newMockFileChecker()
	fileChecker.existsMap[FreedomAppPath] = true

	protector := NewFreedomProtectorWithDeps(pm, zap.NewNop(), cmdRunner, fileChecker)

	health := protector.GetHealth()

	assert.True(t, health.Installed)
	assert.True(t, health.AppRunning)
	assert.True(t, health.ProxyRunning)
	assert.True(t, health.HelperRunning)
	assert.True(t, health.LoginItemPresent)
	assert.Equal(t, FreedomProxyPort, health.ProxyPort)
}

// TestFreedomProtectorImpl_Protect tests protection cycle
func TestFreedomProtectorImpl_Protect(t *testing.T) {
	tests := []struct {
		name             string
		installed        bool
		appRunning       bool
		loginItemPresent bool
		helperRunning    bool
		restartError     error
		restoreError     error
		expectedAction   bool
		expectedErr      bool
	}{
		{
			name:           "not installed - skip",
			installed:      false,
			expectedAction: false,
			expectedErr:    false,
		},
		{
			name:             "all healthy - no action",
			installed:        true,
			appRunning:       true,
			loginItemPresent: true,
			helperRunning:    true,
			expectedAction:   false,
			expectedErr:      false,
		},
		{
			name:             "app not running - restart",
			installed:        true,
			appRunning:       false,
			loginItemPresent: true,
			helperRunning:    true,
			expectedAction:   true,
			expectedErr:      false,
		},
		{
			name:             "login item missing - restore",
			installed:        true,
			appRunning:       true,
			loginItemPresent: false,
			helperRunning:    true,
			expectedAction:   true,
			expectedErr:      false,
		},
		{
			name:             "both app and login item need fixing",
			installed:        true,
			appRunning:       false,
			loginItemPresent: false,
			helperRunning:    true,
			expectedAction:   true,
			expectedErr:      false,
		},
		{
			name:             "restart fails",
			installed:        true,
			appRunning:       false,
			loginItemPresent: true,
			restartError:     errors.New("restart failed"),
			expectedAction:   false,
			expectedErr:      true,
		},
		{
			name:             "restore login item fails",
			installed:        true,
			appRunning:       true,
			loginItemPresent: false,
			restoreError:     errors.New("restore failed"),
			expectedAction:   false,
			expectedErr:      true,
		},
		{
			name:             "helper not running - log warning only",
			installed:        true,
			appRunning:       true,
			loginItemPresent: true,
			helperRunning:    false,
			expectedAction:   false,
			expectedErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := newMockPMForFreedom()
			cmdRunner := newMockCommandRunner()
			fileChecker := newMockFileChecker()

			// Setup installed state
			fileChecker.existsMap[FreedomAppPath] = tt.installed

			// Setup app running state
			if tt.appRunning {
				pm.findResults["Freedom"] = []int{100}
				cmdRunner.outputMap["ps -p"] = []byte("Freedom\n")
			}

			// Setup login item state
			if tt.loginItemPresent {
				cmdRunner.outputMap["osascript -e"] = []byte("Freedom\n")
			} else {
				cmdRunner.outputMap["osascript -e"] = []byte("\n")
			}

			// Setup helper running state
			if tt.helperRunning {
				pm.findResults["com.80pct.FreedomHelper"] = []int{815}
			} else {
				cmdRunner.outputError["pgrep -f"] = errors.New("not found")
			}

			// Setup errors
			if tt.restartError != nil {
				cmdRunner.runError = tt.restartError
			}
			if tt.restoreError != nil && tt.restartError == nil {
				cmdRunner.runError = tt.restoreError
			}

			protector := NewFreedomProtectorWithDeps(pm, zap.NewNop(), cmdRunner, fileChecker)

			actionTaken, err := protector.Protect()

			assert.Equal(t, tt.expectedAction, actionTaken)
			if tt.expectedErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestFreedomProtectorImpl_NilLogger tests nil logger handling
func TestFreedomProtectorImpl_NilLogger(t *testing.T) {
	pm := newMockPMForFreedom()
	cmdRunner := newMockCommandRunner()
	fileChecker := newMockFileChecker()

	// Setup scenario that triggers all log methods
	fileChecker.existsMap[FreedomAppPath] = true
	cmdRunner.outputMap["osascript -e"] = []byte("\n")          // no login item
	cmdRunner.outputError["pgrep -f"] = errors.New("not found") // no helper

	protector := NewFreedomProtectorWithDeps(pm, nil, cmdRunner, fileChecker) // nil logger

	// All these should not panic with nil logger
	_ = protector.IsInstalled()
	_ = protector.IsAppRunning()
	_ = protector.IsProxyRunning()
	_ = protector.IsHelperRunning()
	_ = protector.IsLoginItemPresent()
	_ = protector.GetHealth()

	// Protect triggers logging in multiple paths
	_, _ = protector.Protect()
}

// TestFreedomHealth_Struct verifies FreedomHealth struct fields
func TestFreedomHealth_Struct(t *testing.T) {
	health := domain.FreedomHealth{
		Installed:        true,
		AppRunning:       true,
		ProxyRunning:     true,
		HelperRunning:    true,
		LoginItemPresent: true,
		ProxyPort:        7769,
	}

	assert.True(t, health.Installed)
	assert.True(t, health.AppRunning)
	assert.True(t, health.ProxyRunning)
	assert.True(t, health.HelperRunning)
	assert.True(t, health.LoginItemPresent)
	assert.Equal(t, 7769, health.ProxyPort)
}

// TestFreedomHealth_ZeroValue verifies FreedomHealth zero values
func TestFreedomHealth_ZeroValue(t *testing.T) {
	health := domain.FreedomHealth{}

	assert.False(t, health.Installed)
	assert.False(t, health.AppRunning)
	assert.False(t, health.ProxyRunning)
	assert.False(t, health.HelperRunning)
	assert.False(t, health.LoginItemPresent)
	assert.Equal(t, 0, health.ProxyPort)
}

// TestFreedomProtectorImpl_Interface verifies interface compliance
func TestFreedomProtectorImpl_Interface(t *testing.T) {
	pm := newMockPMForFreedom()
	protector := NewFreedomProtector(pm, zap.NewNop())

	var fp domain.FreedomProtector = protector
	_ = fp
}

// TestRealCommandRunner verifies real command runner (integration)
func TestRealCommandRunner(t *testing.T) {
	runner := &RealCommandRunner{}

	// Test Output with a simple command
	output, err := runner.Output("echo", "hello")
	assert.NoError(t, err)
	assert.Equal(t, "hello\n", string(output))

	// Test Run with a simple command
	err = runner.Run("true")
	assert.NoError(t, err)
}

// TestRealFileChecker verifies real file checker (integration)
func TestRealFileChecker(t *testing.T) {
	checker := &RealFileChecker{}

	// Test existing path
	assert.True(t, checker.Exists("/"))

	// Test non-existing path
	assert.False(t, checker.Exists("/nonexistent/path/xyz123"))
}

// TestIsExactProcessMatch_EdgeCases tests edge cases in process matching
func TestIsExactProcessMatch_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		psOutput string
		expected bool
	}{
		{"exact match", "Freedom", true},
		{"path suffix match", "/Applications/Freedom.app/Contents/MacOS/Freedom", true},
		{"FreedomProxy should not match", "FreedomProxy", false},
		{"FreedomHelper should not match", "FreedomHelper", false},
		{"unrelated process", "Finder", false},
		{"empty output", "", false},
		{"whitespace only", "  \n\t  ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := newMockPMForFreedom()
			pm.findResults["Freedom"] = []int{12345}

			cmdRunner := newMockCommandRunner()
			cmdRunner.outputMap["ps -p"] = []byte(tt.psOutput + "\n")

			protector := NewFreedomProtectorWithDeps(pm, zap.NewNop(), cmdRunner, newMockFileChecker())

			result := protector.IsAppRunning()
			assert.Equal(t, tt.expected, result)
		})
	}
}
