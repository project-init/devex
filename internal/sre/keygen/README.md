# Keygen

The keygen cmd is meant to generate a random character string that can be used as an api key.

## Configuration

```yaml
keygen:
  length: 32
```

## Usage

```shell
devex sre keygen
devex sre keygen --length 48
KEY=$(devex sre keygen)
```

#### Description

The keygen cmd draws `--length` random bytes from `crypto/rand` (default: `keygen.length` from the configuration, else 32) and prints them base64url-encoded without padding, so 32 bytes make a 43-character key. The length counts bytes,
not characters, and must be at least 16. On a terminal it labels the key; when stdout is a pipe or file it prints only
the key, so command substitution captures it. `--output json` prints `{"key": ..., "bytes": ...}`.
