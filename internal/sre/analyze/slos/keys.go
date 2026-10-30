package slos

import "fmt"

const (
	apiKeyEnv = "DD_API_KEY"
	appKeyEnv = "DD_APP_KEY"
)

// loadKeys reads the Datadog keys from the environment. devex does not fetch them: a key in the shell is one thing
// to keep working, where a secret to read is an AWS profile, a session, and a role as well.
func loadKeys(getenv func(string) string) (apiKey, appKey string, err error) {
	apiKey, appKey = getenv(apiKeyEnv), getenv(appKeyEnv)
	var missing []string
	for _, e := range []struct{ name, value string }{{apiKeyEnv, apiKey}, {appKeyEnv, appKey}} {
		if e.value == "" {
			missing = append(missing, e.name)
		}
	}
	switch len(missing) {
	case 0:
		return apiKey, appKey, nil
	case 1:
		return "", "", fmt.Errorf("export %s: the report needs a Datadog API key and an app key with the timeseries_query scope", missing[0])
	}
	return "", "", fmt.Errorf("export %s and %s: the report needs a Datadog API key and an app key with the timeseries_query scope", missing[0], missing[1])
}
