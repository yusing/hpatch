Fix Flask's development server address selection when `SERVER_NAME` contains a bracketed IPv6 address, with or without an explicit port.

The configured hostname and port must be parsed without treating IPv6 colons as the host-port separator. Preserve existing behavior for DNS names, IPv4 addresses, explicit `host` or `port` arguments including port zero, debug options, and server shutdown cleanup.

Only `src/flask/app.py` may be changed. Do not modify or add tests, documentation, dependencies, generated files, or any other production path.
