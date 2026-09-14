# Canonical provider names

New configurations use `digibugcat/obs`. The Move filter resources (`obs_move_*`)
are part of the same provider since 0.1.7; the separate `digibugcat/obs-move` provider
is retired, and an existing workspace migrates with
`terraform state replace-provider registry.terraform.io/digibugcat/obs-move registry.terraform.io/digibugcat/obs`.
These addresses identify the local providers; they are not published Registry
releases. The Office example's local filesystem mirror supports `terraform init`.

```hcl
terraform {
  required_providers {
    obs      = { source = "digibugcat/obs" }
    obs-move = { source = "digibugcat/obs-move" }
  }
}
provider "obs" {}
provider "obs-move" {}
```

Base resources have the `obs_` prefix. Typed Move resources are
`obs_move_source_filter`, `obs_move_value_filter`, and `obs_move_plugin`.
Every Move resource must include `provider = obs-move`: Terraform otherwise
infers the base `obs` provider from its first underscore. Terraform provider
local names cannot contain underscores; resource type names can.

Build the base provider from this repository root (`go build .`) and the Move provider from
[`terraform-provider-obs-move`](https://github.com/DigiBugCat/terraform-provider-obs-move). The helper CLI remains
`obstf` (renamed from the stand-in `obsiac`); that name is not a Terraform source address.

## Existing state

A source-address rename and a resource-type rename are different operations.
`terraform state replace-provider` changes only the provider address; it does
not convert `obsiac_scene` into `obs_scene`. No automatic state migration has
been performed or qualified. Do not apply a renamed example over historical
state: it can appear to remove old objects and create new ones.

Continue an existing workspace with its original configuration and the legacy
`archive/legacy-obsiac/cmd/terraform-provider-obsiac` or `.../terraform-provider-obsmove` binary (their workspaces live beside them under `archive/legacy-obsiac/examples/`). Those
commands preserve the original provider and resource names. The completed
provider work and accepted demo are preserved under `archive/`.

For new authoring, use a fresh workspace and an isolated OBS collection, as in
the Office example. Migration of an existing collection requires a state backup
and a reviewed resource-by-resource import/ownership handoff using each
resource's documented import identity. A blanket cross-type `state mv` or
`replace-provider` command is not a supported migration recipe.
