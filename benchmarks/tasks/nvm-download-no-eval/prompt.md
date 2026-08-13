Harden the `install.sh` implementation of `nvm_download` when it translates curl-style arguments for wget.

The wget path must pass every URL and output path as literal argument data, without reparsing shell syntax. Preserve the existing curl path and wget translations for progress, compression, failure, redirect, headers, silence, output, and resume flags. An argument following `-C` must be consumed as that option's value rather than forwarded independently. Paths containing spaces and URLs containing shell metacharacters must remain one unchanged argument.

Only `install.sh` may be changed. Keep the implementation POSIX-shell compatible. Do not modify or add tests, documentation, dependencies, generated files, or any other production path.
