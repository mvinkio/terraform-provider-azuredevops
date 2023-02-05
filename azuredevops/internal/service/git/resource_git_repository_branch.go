package git

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v6/git"
	"github.com/microsoft/terraform-provider-azuredevops/azuredevops/internal/client"
	"github.com/microsoft/terraform-provider-azuredevops/azuredevops/internal/utils"
	"github.com/microsoft/terraform-provider-azuredevops/azuredevops/internal/utils/converter"
	"github.com/microsoft/terraform-provider-azuredevops/azuredevops/internal/utils/tfhelper"
)

// ResourceGitRepositoryBranch schema to manage the lifecycle of a git repository branch
func ResourceGitRepositoryBranch() *schema.Resource {
	return &schema.Resource{
		Schema: map[string]*schema.Schema{
			"name": {
				Description:  "The name of this branch",
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.NoZeroValues,
				Sensitive:    false,
			},
			"repository_id": {
				Description:  "The uuid of the repository where the branch lives.",
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.IsUUID,
			},
			"ref": {
				Description:  "The ref which the branch is created from. If not given will initialise an orphan branch.",
				Type:         schema.TypeString,
				Optional:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringIsNotEmpty,
			},
			"default": {
				Description: "Bool, true if the branch is the default branch of the git repository.",
				Type:        schema.TypeBool,
				Computed:    true,
			},
		},
		CreateContext: resourceGitRepositoryBranchCreate,
		ReadContext:   resourceGitRepositoryBranchRead,
		DeleteContext: resourceGitRepositoryBranchDelete,
		Importer: &schema.ResourceImporter{
			StateContext: func(ctx context.Context, d *schema.ResourceData, m interface{}) ([]*schema.ResourceData, error) {
				repoId, branchName, err := tfhelper.ParseGitRepoBranchID(d.Id())
				if err != nil {
					return nil, err
				}

				clients := m.(*client.AggregatedClient)
				branchStats, err := clients.GitReposClient.GetBranch(clients.Ctx, git.GetBranchArgs{
					RepositoryId: converter.String(repoId),
					Name:         converter.String(branchName),
				})
				if err != nil {
					return nil, fmt.Errorf("Error checking if branch %q exists: %w", branchName, err)
				}

				d.SetId(fmt.Sprintf("%s:%s", repoId, branchName))
				d.Set("name", branchName)
				d.Set("repository_id", repoId)
				d.Set("default", *branchStats.IsBaseVersion)

				return []*schema.ResourceData{d}, nil
			},
		},
	}
}

func resourceGitRepositoryBranchCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	clients := m.(*client.AggregatedClient)

	repoId := d.Get("repository_id").(string)
	name := d.Get("name").(string)

	if ref, ok := d.GetOk("ref"); !ok {
		args := branchCreatePushArgs(withRefsHeadsPrefix(name), repoId)

		_, err := clients.GitReposClient.CreatePush(clients.Ctx, args)
		if err != nil {
			return diag.FromErr(fmt.Errorf("Error initialising new branch: %w", err))
		}
	} else {
		ref := ref.(string)

		// Azuredevops GetRefs api returns refs whose "prefix" matches Filter sorted from shortest to longest
		// Top1 should return best match
		gotRefs, err := clients.GitReposClient.GetRefs(clients.Ctx, git.GetRefsArgs{
			RepositoryId: converter.String(repoId),
			Filter:       converter.String(strings.TrimPrefix(ref, "refs/")),
			Top:          converter.Int(1),
			PeelTags:     converter.Bool(true),
		})
		if err != nil {
			return diag.FromErr(fmt.Errorf("Error getting refs matching %q: %w", ref, err))
		}

		if len(gotRefs.Value) == 0 {
			return diag.FromErr(fmt.Errorf("No refs found that match %q.", ref))
		}

		gotRef := gotRefs.Value[0]
		if gotRef.Name == nil {
			return diag.FromErr(fmt.Errorf("Got unexpected GetRefs response, a ref without a name was returned."))
		}

		// Check for complete match. Sometimes refs exist that match prefix with Ref, but do not match completely.
		if *gotRef.Name != ref {
			return diag.FromErr(fmt.Errorf("Ref %q not found, closest match is %q.", ref, *gotRef.Name))
		}

		// Check if ref was a tag and we need to use PeeledObjectId to get the commit id of the tag
		var commit *string
		if gotRef.PeeledObjectId != nil {
			commit = gotRef.PeeledObjectId
		} else if gotRef.ObjectId != nil {
			commit = gotRef.ObjectId
		} else {
			return diag.FromErr(fmt.Errorf("GetRefs response doesn't have a valid commit id."))
		}

		_, err = updateRefs(clients, git.UpdateRefsArgs{
			RefUpdates: &[]git.GitRefUpdate{{
				Name:        converter.String(withRefsHeadsPrefix(name)),
				NewObjectId: commit,
				OldObjectId: converter.String("0000000000000000000000000000000000000000"),
			}},
			RepositoryId: converter.String(repoId),
		})
		if err != nil {
			return diag.FromErr(fmt.Errorf("Error creating branch against ref %q: %w", ref, err))
		}
	}

	d.SetId(fmt.Sprintf("%s:%s", repoId, name))

	return resourceGitRepositoryBranchRead(ctx, d, m)
}

func resourceGitRepositoryBranchRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	clients := m.(*client.AggregatedClient)

	repoId, name, err := tfhelper.ParseGitRepoBranchID(d.Id())
	if err != nil {
		return diag.FromErr(err)
	}

	branchStats, err := clients.GitReposClient.GetBranch(clients.Ctx, git.GetBranchArgs{
		RepositoryId: converter.String(repoId),
		Name:         converter.String(name),
	})
	if err != nil {
		if utils.ResponseWasNotFound(err) {
			d.SetId("")
			return nil
		}
		return diag.FromErr(fmt.Errorf("Error reading branch %q: %w", name, err))
	}

	d.SetId(fmt.Sprintf("%s:%s", repoId, name))
	d.Set("name", name)
	d.Set("repository_id", repoId)
	d.Set("default", *branchStats.IsBaseVersion)

	return nil
}

func resourceGitRepositoryBranchDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	clients := m.(*client.AggregatedClient)

	repoId, name, err := tfhelper.ParseGitRepoBranchID(d.Id())
	if err != nil {
		return diag.FromErr(err)
	}

	branchStats, err := clients.GitReposClient.GetBranch(clients.Ctx, git.GetBranchArgs{
		RepositoryId: converter.String(repoId),
		Name:         converter.String(name),
	})
	if err != nil {
		return diag.FromErr(fmt.Errorf("Error getting latest commit of %q: %w", name, err))
	}

	_, err = updateRefs(clients, git.UpdateRefsArgs{
		RefUpdates: &[]git.GitRefUpdate{{
			Name:        converter.String(withRefsHeadsPrefix(name)),
			OldObjectId: branchStats.Commit.CommitId,
			NewObjectId: converter.String("0000000000000000000000000000000000000000"),
		}},
		RepositoryId: converter.String(repoId),
	})
	if err != nil {
		return diag.FromErr(fmt.Errorf("Error deleting branch %q: %w", name, err))
	}

	return nil
}

func branchCreatePushArgs(name, repoId string) git.CreatePushArgs {
	args := git.CreatePushArgs{
		RepositoryId: converter.String(repoId),
		Push: &git.GitPush{
			RefUpdates: &[]git.GitRefUpdate{
				{
					Name:        converter.String(name),
					OldObjectId: converter.String("0000000000000000000000000000000000000000"),
				},
			},
			Commits: &[]git.GitCommitRef{
				{
					Comment: converter.String("Initial commit."),
					Changes: &[]interface{}{
						git.Change{
							ChangeType: &git.VersionControlChangeTypeValues.Add,
							Item: git.GitItem{
								Path: converter.String("/readme.md"),
							},
							NewContent: &git.ItemContent{
								ContentType: &git.ItemContentTypeValues.RawText,
								Content:     converter.String("Branch initialized with azuredevops terraform provider"),
							},
						},
					},
				},
			},
		},
	}
	return args
}

func updateRefs(clients *client.AggregatedClient, args git.UpdateRefsArgs) (*[]git.GitRefUpdateResult, error) {
	updateRefResults, err := clients.GitReposClient.UpdateRefs(clients.Ctx, args)
	if err != nil {
		return nil, err
	}

	for _, refUpdate := range *updateRefResults {
		if !*refUpdate.Success {
			return nil, fmt.Errorf("Error got invalid GitRefUpdate.UpdateStatus: %s", *refUpdate.UpdateStatus)
		}
	}

	return updateRefResults, nil
}

func withRefsHeadsPrefix(branchName string) string {
	prefix := "refs/heads/"
	if strings.HasPrefix(branchName, prefix) {
		return branchName
	}
	return prefix + branchName
}
