package errtrack

import "fmt"

const (
	apiKeyEnv = "DD_API_KEY"
	appKeyEnv = "DD_APP_KEY"
)

// keyAdvice is the tail of every missing-key message.
//
// slos names the scope its app key needs, timeseries_query. The Error Tracking
// endpoints are scoped too, but the OpenAPI spec carries no x-permission for
// them and Datadog does not document the name, so this stays general. A 403
// body names the missing scope — put it here the first time anyone sees one.
const keyAdvice = "errtrack needs a Datadog API key and an app key with access to Error Tracking"

// loadKeys reads the Datadog keys from the environment.
//
// errtrack does not fetch them from a secret store: a key in the shell is one
// thing to keep working, where a secret to read is a profile, a session and a
// role as well. It is also what keeps this tool usable outside DailyPay.
//
// getenv is a parameter rather than os.Getenv so a test can supply an
// environment instead of mutating the process, and so the command can pass the
// one from its deps.
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
		// Name the one that is missing: the other is set, so a typo in its
		// name is the likeliest cause and the pair looks fine at a glance.
		return "", "", fmt.Errorf("export %s: %s", missing[0], keyAdvice)
	}

	return "", "", fmt.Errorf("export %s and %s: %s", missing[0], missing[1], keyAdvice)
}
