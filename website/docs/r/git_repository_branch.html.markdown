---
layout: "azuredevops"
page_title: "AzureDevops: azuredevops_git_repository_branch"
description: |-
  Manages a Git Repository Branch.
---

# azuredevops_git_repository_branch

Manages a Git Repository Branch.

## Example Usage

```hcl
resource "azuredevops_project" "example" {
  name               = "Example Project"
  visibility         = "private"
  version_control    = "Git"
  work_item_template = "Agile"
}

resource "azuredevops_git_repository" "example" {
  project_id = azuredevops_project.example.id
  name       = "Example Git Repository"
  initialization {
    init_type = "Uninitialized"
  }
}

resource "azuredevops_git_repository_branch" "example_orphan" {
  repository_id = azuredevops_git_repository.example.id
  name = "master"
}

resource "azuredevops_git_repository_branch" "example_from_ref" {
  repository_id = azuredevops_git_repository.example.id
  name = "develop"
  ref = "refs/heads/${azuredevops_git_repository_branch.example_orphan.name}"
}
```

## Arguments Reference

The following arguments are supported:

- `name` - (Required) The name of the branch (not prefixed with `refs/heads/`).

- `repository_id` - (Required) The ID of the repository the branch is created against.

---

- `ref` - (Optional) The ref the branch is created from. (prefixed with `refs/heads/` or `refs/tags/`)

## Attributes Reference

In addition to the Arguments listed above - the following Attributes are exported:

- `id` - The ID of the Git Repository Branch.

- `default` - True if the branch is the default branch of the git repository, false otherwise.

## Import

Git Repository Branches can be imported using the `resource id`, e.g.

```shell
terraform import azuredevops_git_repository_branch.example 00000000-0000-0000-0000-000000000000:master
```
