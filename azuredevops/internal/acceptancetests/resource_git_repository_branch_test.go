//go:build (all || core || resource_git_repository_branch) && !exclude_resource_git_repository_branch
// +build all core resource_git_repository_branch
// +build !exclude_resource_git_repository_branch

package acceptancetests

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v6/git"
	"github.com/microsoft/terraform-provider-azuredevops/azuredevops/internal/acceptancetests/testutils"
	"github.com/microsoft/terraform-provider-azuredevops/azuredevops/internal/client"
	"github.com/microsoft/terraform-provider-azuredevops/azuredevops/internal/utils/tfhelper"
)

// TestAccGitRepoBranch_CreateUpdateDelete verifies that a branch can
// be added to a repository and that it can be replaced
func TestAccGitRepoBranch_CreateAndUpdate(t *testing.T) {
	var gotBranch git.GitBranchStats
	var gotBranch2 git.GitBranchStats
	var gotBranch3 git.GitBranchStats
	projectName := testutils.GenerateResourceName()
	gitRepoName := testutils.GenerateResourceName()
	branchName := testutils.GenerateResourceName()
	branchNameChanged := testutils.GenerateResourceName()

	resource.Test(t, resource.TestCase{
		PreCheck:  func() { testutils.PreCheck(t, nil) },
		Providers: testutils.GetProviders(),
		Steps: []resource.TestStep{
			{
				Config: hclGitRepoBranches(projectName, gitRepoName, "Clean", branchName),
				Check: resource.ComposeTestCheckFunc(
					testAccGitRepoBranchExists("foo_orphan", &gotBranch),
					testAccGitRepoBranchExists("foo_from_ref", &gotBranch2),
					testAccGitRepoBranchExists("foo_from_sha", &gotBranch3),
					testAccGitRepoBranchAttributes("foo_orphan", &gotBranch, &testAccGitRepoBranchExpectedAttributes{
						Name: fmt.Sprintf("testbranch-%s", branchName),
					}, &testAccExpectedComputedStateAttrs{
						source_ref: "",
						source_sha: "",
					}),
					testAccGitRepoBranchAttributes("foo_from_ref", &gotBranch2, &testAccGitRepoBranchExpectedAttributes{
						Name: fmt.Sprintf("testbranch2-%s", branchName),
					}, &testAccExpectedComputedStateAttrs{
						source_ref: fmt.Sprintf("refs/heads/testbranch-%s", branchName),
						source_sha: *gotBranch.Commit.CommitId,
					}),
					testAccGitRepoBranchAttributes("foo_from_sha", &gotBranch3, &testAccGitRepoBranchExpectedAttributes{
						Name: fmt.Sprintf("testbranch3-%s", branchName),
					}, &testAccExpectedComputedStateAttrs{
						source_ref: "",
						source_sha: *gotBranch.Commit.CommitId,
					}),
				),
			},
			// Test import branch created from another branch
			{
				ResourceName:            "azuredevops_git_repository_branch.foo_from_ref",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"ref"},
			},
			// Test replace/update branch when name changes
			// {
			// 	Config: hclGitRepoBranches(projectName, gitRepoName, "Clean", branchNameChanged),
			// 	Check: resource.ComposeTestCheckFunc(
			// 		testAccGitRepoBranchExists(node("foo_orphan"), &gotBranch),
			// 		testAccGitRepoBranchExists(node("foo_from_ref"), &gotBranch2),
			// 		testAccGitRepoBranchExists(node("foo_from_sha"), &gotBranch3),
			// 		testAccGitRepoBranchComputedState(node("foo_orphan"), &testAccExpectedComputedStateAttrs{
			// 			source_ref: "",
			// 			source_sha: false,
			// 		}),
			// 		testAccGitRepoBranchComputedState(node("foo_from_ref"), &testAccExpectedComputedStateAttrs{
			// 			source_ref: fmt.Sprintf("refs/heads/testbranch-%s", branchName),
			// 			source_sha: true,
			// 		}),
			// 		testAccGitRepoBranchComputedState(node("foo_from_sha"), &testAccExpectedComputedStateAttrs{
			// 			source_sha: true,
			// 		}),
			// 		testAccGitRepoBranchAttributes("foo_orphan", &gotBranch, &testAccGitRepoBranchExpectedAttributes{
			// 			Name:    fmt.Sprintf("testbranch-%s", branchNameChanged),
			// 			Default: false,
			// 		}),
			// 		testAccGitRepoBranchAttributes("foo_from_ref", &gotBranch2, &testAccGitRepoBranchExpectedAttributes{
			// 			Name:    fmt.Sprintf("testbranch2-%s", branchNameChanged),
			// 			Default: false,
			// 		}),
			// 		testAccGitRepoBranchAttributes("foo_from_sha", &gotBranch3, &testAccGitRepoBranchExpectedAttributes{
			// 			Name:    fmt.Sprintf("testbranch3-%s", branchNameChanged),
			// 			Default: false,
			// 		}),
			// 	),
			// },
			// Test invalid ref
			{
				Config: fmt.Sprintf(`
%s

resource "azuredevops_git_repository_branch" "foo_nonexistent_tag" {
	repository_id = azuredevops_git_repository.repository.id
    name = "testbranch2-non-existent-tag"
	ref = "refs/tags/non-existent"
}
`, hclGitRepoBranches(projectName, gitRepoName, "Clean", branchNameChanged)),
				ExpectError: regexp.MustCompile(`No source refs found that match "refs/tags/non-existent"`),
			},
		},
	},
	)
}

func testAccGitRepoBranchAttributes(branchName string, branch *git.GitBranchStats, want *testAccGitRepoBranchExpectedAttributes, wantComputed *testAccExpectedComputedStateAttrs) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		if *branch.Name != want.Name {
			return fmt.Errorf("Error got name %s, want %s", *branch.Name, want.Name)
		}
		return nil
	}
}

func testAccGitRepoBranchExists(node string, gotBranch *git.GitBranchStats) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[node]
		if !ok {
			return fmt.Errorf("Not found: %s", node)
		}

		repoID, branchName, err := tfhelper.ParseGitRepoBranchID(rs.Primary.ID)
		if err != nil {
			return fmt.Errorf("Error in parsing branch ID: %w", err)
		}

		clients := testutils.GetProvider().Meta().(*client.AggregatedClient)
		branch, err := clients.GitReposClient.GetBranch(clients.Ctx, git.GetBranchArgs{
			RepositoryId: &repoID,
			Name:         &branchName,
		})
		if err != nil {
			return err
		}
		*gotBranch = *branch

		return nil
	}
}

func hclGitRepoBranches(projectName, gitRepoName, initType, branchName string) string {
	gitRepoResource := testutils.HclGitRepoResource(projectName, gitRepoName, initType)
	return fmt.Sprintf(`
%[1]s

resource "azuredevops_git_repository_branch" "foo_orphan" {
	repository_id = azuredevops_git_repository.repository.id
	name = "testbranch-%[2]s"
}
resource "azuredevops_git_repository_branch" "foo_from_ref" {
	repository_id = azuredevops_git_repository.repository.id
    name = "testbranch2-%[2]s"
	source_ref = azuredevops_git_repository_branch.foo_orphan.ref
}
resource "azuredevops_git_repository_branch" "foo_from_sha" {
	repository_id = azuredevops_git_repository.repository.id
    name = "testbranch3-%[2]s"
	source_sha = azuredevops_git_repository_branch.foo_orphan.sha
}
  `, gitRepoResource, branchName)
}

type testAccExpectedComputedStateAttrs struct {
	source_ref string
	source_sha string
}

type testAccGitRepoBranchExpectedAttributes struct {
	Name string
}
