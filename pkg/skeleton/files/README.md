# nimble config

This repo is a [nimble](https://github.com/nimblescape/nimble) config repo, checked out at
`~/.nimble` on the workstation. Hosts get a snapshot of what they need with every
`nimble update <host>`; they have no access to this repo.

- `nimble.yaml`: the default template, the administration domains, shared links
- `hosts/`: one YAML file per machine, with its tools and login shell (`nimble host create`)
- `templates/`: starting points that `nimble host create` copies into a host file (`nimble template create`)
- `keys/`: the public keys of your hardware keys (`nimble key enclave`, `nimble key yubikey`)
- `toolchain/pins.yaml`: pinned versions, updated by the daily workflow
- `shell/`: shared shell files for bash and zsh; `personal/`: your own layer

Change things with nimble's commands, then `nimble push`; machines pick them up with
`nimble update`.
