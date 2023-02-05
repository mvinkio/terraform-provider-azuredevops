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
	projectName := testutils.GenerateResourceName()
	gitRepoName := testutils.GenerateResourceName()
	branchName := testutils.GenerateResourceName()
	branchNameChanged := testutils.GenerateResourceName()

	node := func(name string) string {
		return fmt.Sprintf("azuredevops_git_repository_branch.%s", name)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:  func() { testutils.PreCheck(t, nil) },
		Providers: testutils.GetProviders(),
		Steps: []resource.TestStep{
			{
				Config: hclGitRepoBranches(projectName, gitRepoName, "Clean", branchName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckNoResourceAttr(node("foo_orphan"), "ref"),
					resource.TestCheckResourceAttr(node("foo_against_ref"), "ref", fmt.Sprintf("refs/heads/testbranch-%s", branchName)),
					testAccGitRepoBranchExists("foo_orphan", &gotBranch),
					testAccGitRepoBranchExists("foo_against_ref", &gotBranch2),
					testAccGitRepoBranchAttributes("foo_orphan", &gotBranch, &testAccGitRepoBranchExpectedAttributes{
						Name:    fmt.Sprintf("testbranch-%s", branchName),
						Default: false,
					}),
					testAccGitRepoBranchAttributes("foo_against_ref", &gotBranch2, &testAccGitRepoBranchExpectedAttributes{
						Name:    fmt.Sprintf("testbranch2-%s", branchName),
						Default: false,
					}),
				),
			},
			// Test import branch created against another branch
			{
				ResourceName:            "azuredevops_git_repository_branch.foo_against_ref",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"ref"},
			},
			// Test replace/update branch when name changes
			{
				Config: hclGitRepoBranches(projectName, gitRepoName, "Clean", branchNameChanged),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckNoResourceAttr(node("foo_orphan"), "ref"),
					resource.TestCheckResourceAttr(node("foo_against_ref"), "ref", fmt.Sprintf("refs/heads/testbranch-%s", branchNameChanged)),
					testAccGitRepoBranchExists("foo_orphan", &gotBranch),
					testAccGitRepoBranchExists("foo_against_ref", &gotBranch2),
					testAccGitRepoBranchAttributes("foo_orphan", &gotBranch, &testAccGitRepoBranchExpectedAttributes{
						Name:    fmt.Sprintf("testbranch-%s", branchNameChanged),
						Default: false,
					}),
					testAccGitRepoBranchAttributes("foo_against_ref", &gotBranch2, &testAccGitRepoBranchExpectedAttributes{
						Name:    fmt.Sprintf("testbranch2-%s", branchNameChanged),
						Default: false,
					}),
				),
			},
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
				ExpectError: regexp.MustCompile(`No refs found that match "refs/tags/non-existent"`),
			},
		},
	},
	)
}

func testAccGitRepoBranchAttributes(s string, branch *git.GitBranchStats, want *testAccGitRepoBranchExpectedAttributes) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		if *branch.Name != want.Name {
			return fmt.Errorf("Error got name %s, want %s", *branch.Name, want.Name)
		}
		if *branch.IsBaseVersion != want.Default {
			return fmt.Errorf("Error got default %v, want %v", *branch.Name, want.Name)
		}
		return nil
	}
}

func testAccGitRepoBranchExists(nodeName string, gotBranch *git.GitBranchStats) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[fmt.Sprintf("azuredevops_git_repository_branch.%s", nodeName)]
		if !ok {
			return fmt.Errorf("Not found: %s", nodeName)
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
resource "azuredevops_git_repository_branch" "foo_against_ref" {
	repository_id = azuredevops_git_repository.repository.id
    name = "testbranch2-%[2]s"
	ref = "refs/heads/${azuredevops_git_repository_branch.foo_orphan.name}"
}
  `, gitRepoResource, branchName)
}

type testAccGitRepoBranchExpectedAttributes struct {
	Name    string
	Default bool
}
