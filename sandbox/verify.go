package sandbox

type VerificationResult struct {
	DriverName        string   `json:"driver_name"`
	SecureMode        bool     `json:"secure_mode"`
	SandboxExecutable string   `json:"sandbox_executable,omitempty"`
	ProfileApplied    bool     `json:"profile_applied"`
	ReadWritePaths    []string `json:"read_write_paths,omitempty"`
	ReadOnlyPaths     []string `json:"read_only_paths,omitempty"`
}

func Verify(driver Driver, secureMode bool) VerificationResult {
	result := VerificationResult{SecureMode: secureMode}
	if driver != nil {
		result.DriverName = driver.Name()
	}
	return result
}
