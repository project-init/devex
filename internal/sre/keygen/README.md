# Keygen

The keygen cmd is meant to generate a random character string that can be used as an api key.

## Configuration

```yaml
keygen:
  length: 32
```

## Usage

```shell
sre keygen
```

#### Description

The keygen cmd gets the length from the configuration file, or defaults to 32 characters. It then uses the crypto
package to generate the secure string before eventually printing it to console to be collected and used as you see fit.
