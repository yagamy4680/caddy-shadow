# caddy-shadow

caddy-shadow is
a [Shadow Testing](https://microsoft.github.io/code-with-engineering-playbook/automated-testing/shadow-testing/) module
for Caddy 2, with full Caddyfile support.

# Features

- Request Mirroring
    - Default 1:1 mirroring
    - Configurable fractional mirroring **(⚠️ Planned)**
- Optional response timing metrics for Prometheus
    - Primary/Shadow Time to First Byte
    - Primary/Shadow Total Response Time
- Optional response comparison
    - Full response body comparison
    - Configurable selective comparison of JSON responses (powered by [itchyny/gojq](https://github.com/itchyny/gojq))
    - Configurable response header comparison **(⚠️ Planned)**
    - Response status comparison **(⚠️ Planned)**
- Reporting features **(⚠️ Planned)**

## Caddyfile

caddy-shadow fully supports Caddyfile as well as native JSON configuration.

### Example File

```caddyfile
{
    metrics
}

http://localhost:8080 {
    shadow {
        metrics shadow
        compare_body
        primary {
            reverse_proxy https://my-old-backend.com
        }
        shadow {
            reverse_proxy https://my-new-backend.com
        }
    }
}
```

### Caddyfile Options

| Name              | Description                                                  | Required? | Takes Arguments?         | Default |
|-------------------|--------------------------------------------------------------|-----------|--------------------------|--------|
| `primary`         | The primary/vcurrent definition                              | Yes       | Yes (subroute)           | *none* |
| `shadow`          | The shadow/vcurrent definition                               | Yes       | Yes (subroute)           | *none* |
| `compare_status`  | Enables response-status comparison **(⚠️ Planned)**          | No        | No                       | false  |
| `compare_headers` | Enables response-status comparison  **(⚠️ Planned)**         | No        | No                       | false  |
| `compare_body`    | Enables response-body comparison                             | No        | No                       | false  |
| `compare_jq`      | Enables jq-based response comparison                         | No        | Yes (list of jq queries) | *none* |
| `no_log`          | Disables logging for mismatched responses                    | No        | No                       | False  |
| `metrics`         | Enables metrics                                              | No        | Yes (Prefix/Namespace)   | *none* |
| `Timeout`         | Set the maximum time that the module will wait for responses | No        | Yes (Duration string)    | 30s    |

