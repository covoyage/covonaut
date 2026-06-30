package browser_providers

import "os"

type CloudBrowserProvider interface {
	ProviderName() string
	IsConfigured() bool
	CreateSession(taskID string) (map[string]string, error)
	CloseSession(sessionID string) error
	EmergencyCleanup(sessionID string)
}

type CloudSessionInfo struct {
	SessionName string
	SessionID   string
	CDPURL      string
	Features    map[string]bool
}

func GetEnv(key string, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

func GetEnvBool(key string, defaultValue bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	return val == "true" || val == "1" || val == "yes"
}
