package git

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v6/git"
	"github.com/microsoft/terraform-provider-azuredevops/azuredevops/internal/client"
	"github.com/microsoft/terraform-provider-azuredevops/azuredevops/internal/utils"
	"github.com/microsoft/terraform-provider-azuredevops/azuredevops/internal/utils/converter"
)

func ResourceGitRepositoryFile() *schema.Resource {
	return &schema.Resource{
		Create: resourceGitRepositoryFileCreate,
		Read:   resourceGitRepositoryFileRead,
		Update: resourceGitRepositoryFileUpdate,
		Delete: resourceGitRepositoryFileDelete,
		Importer: &schema.ResourceImporter{
			State: func(d *schema.ResourceData, m interface{}) ([]*schema.ResourceData, error) {
				parts := strings.Split(d.Id(), ":")
				branch := "refs/heads/master"

				if len(parts) > 2 {
					return nil, fmt.Errorf("Invalid ID specified. Supplied ID must be written as <repository>/<file path> (when branch is \"master\") or <repository>/<file path>:<branch>")
				}

				if len(parts) == 2 {
					branch = parts[1]
				}

				clients := m.(*client.AggregatedClient)
				repoID, file := splitRepoFilePath(parts[0])
				if err := checkRepositoryFileExists(clients, repoID, file, branch); err != nil {
					return nil, fmt.Errorf("Repository not found, repository ID: %s, branch: %s, file: %s. Error:  %+v", repoID, branch, file, err)
				}

				d.SetId(fmt.Sprintf("%s/%s", repoID, file))
				d.Set("branch", branch)
				d.Set("overwrite_on_create", false)

				return []*schema.ResourceData{d}, nil
			},
		},

		Schema: map[string]*schema.Schema{
			"repository_id": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				Description:  "The repository ID",
				ValidateFunc: validation.IsUUID,
			},
			"file": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The file path to manage",
			},
			"content": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The file's content",
			},
			"branch": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Description: "The branch name, defaults to \"refs/heads/master\"",
				Default:     "refs/heads/master",
			},
			"commit_message": {
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				Description: "The commit message when creating or updating the file",
			},
			"overwrite_on_create": {
				Type:        schema.TypeBool,
				Optional:    true,
				Description: "Enable overwriting existing files, defaults to \"false\"",
				Default:     false,
			},
		},
		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(1 * time.Minute),
			Read:   schema.DefaultTimeout(5 * time.Second),
		},
	}
}

func resourceGitRepositoryFileCreate(d *schema.ResourceData, m interface{}) error {
	ctx := context.Background()
	clients := m.(*client.AggregatedClient)

	repoId := d.Get("repository_id").(string)
	file := d.Get("file").(string)
	branch := d.Get("branch").(string)
	overwriteOnCreate := d.Get("overwrite_on_create").(bool)

	if err := checkRepositoryBranchExists(clients, repoId, branch); err != nil {
		return err
	}
	version := shortBranchName(branch)
	repoItem, err := clients.GitReposClient.GetItem(ctx, git.GetItemArgs{
		RepositoryId: &repoId,
		Path:         &file,
		VersionDescriptor: &git.GitVersionDescriptor{
			Version:     &version,
			VersionType: &git.GitVersionTypeValues.Branch,
		},
	})
	if err != nil && !utils.ResponseWasNotFound(err) {
		return fmt.Errorf("Repository branch not found, repositoryID: %s, branch: %s. Error:  %+v", repoId, branch, err)
	}

	// Change type should be edit if overwrite is enabled when file exists
	changeType := git.VersionControlChangeTypeValues.Add
	if repoItem != nil {
		if !overwriteOnCreate {
			return fmt.Errorf("Refusing to overwrite existing file. Configure `overwrite_on_create` to `true` to override.")
		}
		changeType = git.VersionControlChangeTypeValues.Edit
	}

	// Need to retry creating the file as multiple updates could happen at the same time
	err = resource.Retry(d.Timeout(schema.TimeoutCreate), func() *resource.RetryError { //nolint:staticcheck
		objectID, err := getLastCommitId(clients, repoId, branch)
		if err != nil {
			return resource.NonRetryableError(err)
		}
		args, err := resourceGitRepositoryPushArgs(d, objectID, changeType)
		if err != nil {
			return resource.NonRetryableError(err)
		}
		if (*args.Push.Commits)[0].Comment == nil {
			m := fmt.Sprintf("Add %s", file)
			(*args.Push.Commits)[0].Comment = &m
		}

		_, err = clients.GitReposClient.CreatePush(ctx, *args)
		if err != nil {
			if utils.ResponseContainsStatusMessage(err, "has already been updated by another client") {
				return resource.RetryableError(err)
			}
			return resource.NonRetryableError(err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("Create repositroy file failed, repositoryID: %s, branch: %s, file: %s. Error:  %+v", repoId, branch, file, err)
	}

	d.SetId(fmt.Sprintf("%s/%s", repoId, file))
	return resourceGitRepositoryFileRead(d, m)
}

func resourceGitRepositoryFileRead(d *schema.ResourceData, m interface{}) error {
	ctx := context.Background()
	clients := m.(*client.AggregatedClient)

	repoId, file := splitRepoFilePath(d.Id())
	branch := d.Get("branch").(string)

	if err := checkRepositoryBranchExists(clients, repoId, branch); err != nil {
		return err
	}

	// Get the repository item if it exists
	repoItem, err := clients.GitReposClient.GetItem(ctx, git.GetItemArgs{
		RepositoryId:   &repoId,
		Path:           &file,
		IncludeContent: converter.Bool(true),
		VersionDescriptor: &git.GitVersionDescriptor{
			Version:     converter.String(shortBranchName(branch)),
			VersionType: &git.GitVersionTypeValues.Branch,
		},
	})
	if err != nil {
		if utils.ResponseWasNotFound(err) {
			d.SetId("")
			return nil
		}
		return fmt.Errorf("Query repository item failed, repositoryID: %s, branch: %s, file: %s . Error:  %+v", repoId, branch, file, err)
	}

	d.Set("content", repoItem.Content)
	d.Set("repository_id", repoId)
	d.Set("file", file)

	commit, err := clients.GitReposClient.GetCommit(ctx, git.GetCommitArgs{
		RepositoryId: &repoId,
		CommitId:     repoItem.CommitId,
	})
	if err != nil {
		return fmt.Errorf("Get repository file commit failed , repositoryID: %s, branch: %s, file: %s . Error:  %+v", repoId, branch, file, err)
	}

	d.Set("commit_message", commit.Comment)

	return nil
}

func resourceGitRepositoryFileUpdate(d *schema.ResourceData, m interface{}) error {
	clients := m.(*client.AggregatedClient)
	ctx := context.Background()

	repoId := d.Get("repository_id").(string)
	file := d.Get("file").(string)
	branch := d.Get("branch").(string)

	if err := checkRepositoryBranchExists(clients, repoId, branch); err != nil {
		return err
	}

	// Need to retry creating the file as multiple updates could happen at the same time
	err := resource.Retry(d.Timeout(schema.TimeoutCreate), func() *resource.RetryError { //nolint:staticcheck
		objectID, err := getLastCommitId(clients, repoId, branch)
		if err != nil {
			return resource.NonRetryableError(err)
		}
		args, err := resourceGitRepositoryPushArgs(d, objectID, git.VersionControlChangeTypeValues.Edit)
		if err != nil {
			return resource.NonRetryableError(err)
		}
		if *(*args.Push.Commits)[0].Comment == fmt.Sprintf("Add %s", file) {
			m := fmt.Sprintf("Update %s", file)
			(*args.Push.Commits)[0].Comment = &m
		}

		_, err = clients.GitReposClient.CreatePush(ctx, *args)
		if err != nil {
			if utils.ResponseContainsStatusMessage(err, "has already been updated by another client") {
				return resource.RetryableError(err)
			}
			return resource.NonRetryableError(err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("Update repository file failed, repositoryID: %s, branch: %s, file: %s . Error:  %+v", repoId, branch, file, err)
	}

	return resourceGitRepositoryFileRead(d, m)
}

func resourceGitRepositoryFileDelete(d *schema.ResourceData, m interface{}) error {
	clients := m.(*client.AggregatedClient)
	ctx := context.Background()

	repoId := d.Get("repository_id").(string)
	file := d.Get("file").(string)
	branch := d.Get("branch").(string)
	message := fmt.Sprintf("Delete %s", file)

	err := resource.Retry(d.Timeout(schema.TimeoutCreate), func() *resource.RetryError { //nolint:staticcheck
		objectID, err := getLastCommitId(clients, repoId, branch)
		if err != nil {
			return resource.NonRetryableError(err)
		}

		change := &git.GitChange{
			ChangeType: &git.VersionControlChangeTypeValues.Delete,
			Item: git.GitItem{
				Path: &file,
			},
		}
		_, err = clients.GitReposClient.CreatePush(ctx, git.CreatePushArgs{
			RepositoryId: &repoId,
			Push: &git.GitPush{
				RefUpdates: &[]git.GitRefUpdate{
					{
						Name:        &branch,
						OldObjectId: &objectID,
					},
				},
				Commits: &[]git.GitCommitRef{
					{
						Comment: &message,
						Changes: &[]interface{}{change},
					},
				},
			},
		})
		if err != nil {
			if utils.ResponseContainsStatusMessage(err, "has already been updated by another client") {
				return resource.RetryableError(err)
			}
			return resource.NonRetryableError(err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to destroy the repository file, repository ID: %s, branch: %s. file %s. Error %+v ", repoId, branch, file, err)
	}
	return nil
}

// checkRepositoryBranchExists tests if a branch exists in a repository.
func checkRepositoryBranchExists(c *client.AggregatedClient, repoId, branch string) error {
	ctx := context.Background()
	_, err := c.GitReposClient.GetBranch(ctx, git.GetBranchArgs{
		RepositoryId: &repoId,
		Name:         converter.String(shortBranchName(branch)),
	})
	return err
}

// checkRepositoryFileExists tests if a file exists in a repository.
func checkRepositoryFileExists(c *client.AggregatedClient, repoId, file, branch string) error {
	ctx := context.Background()
	_, err := c.GitReposClient.GetItem(ctx, git.GetItemArgs{
		RepositoryId: &repoId,
		Path:         &file,
		VersionDescriptor: &git.GitVersionDescriptor{
			Version: converter.String(shortBranchName(branch)),
		},
	})
	if err != nil {
		return err
	}
	return nil
}

// getLastCommitId returns the last commit id in the given branhc and repository.
func getLastCommitId(c *client.AggregatedClient, repoId, branch string) (string, error) {
	ctx := context.Background()
	commits, err := c.GitReposClient.GetCommits(ctx, git.GetCommitsArgs{
		RepositoryId: &repoId,
		Top:          converter.Int(1),
		SearchCriteria: &git.GitQueryCommitsCriteria{
			ItemVersion: &git.GitVersionDescriptor{
				Version: converter.String(shortBranchName(branch)),
			},
		},
	})
	if err != nil {
		return "", err
	}
	return *(*commits)[0].CommitId, nil
}

// resourceGitRepositoryPushArgs returns args used to commit and push changes.
func resourceGitRepositoryPushArgs(d *schema.ResourceData, objectID string, changeType git.VersionControlChangeType) (*git.CreatePushArgs, error) {
	var message *string
	if commitMessage, hasCommitMessage := d.GetOk("commit_message"); hasCommitMessage {
		cm := commitMessage.(string)
		message = &cm
	}

	repo := d.Get("repository_id").(string)
	content := d.Get("content").(string)
	file := d.Get("file").(string)
	branch := d.Get("branch").(string)

	change := git.GitChange{
		ChangeType: &changeType,
		Item: git.GitItem{
			Path: &file,
		},
		NewContent: &git.ItemContent{
			Content:     &content,
			ContentType: &git.ItemContentTypeValues.RawText,
		},
	}
	args := &git.CreatePushArgs{
		RepositoryId: &repo,
		Push: &git.GitPush{
			RefUpdates: &[]git.GitRefUpdate{
				{
					Name:        &branch,
					OldObjectId: &objectID,
				},
			},
			Commits: &[]git.GitCommitRef{
				{
					Comment: message,
					Changes: &[]interface{}{change},
				},
			},
		},
	}
	return args, nil
}

// shortBranchName removes the branch prefix which some API endpoints require.
func shortBranchName(branch string) string {
	return strings.TrimPrefix(branch, "refs/heads/")
}

// splitRepoFilePath splits the resource ID into separate repository id and file path components.
func splitRepoFilePath(path string) (string, string) {
	parts := strings.Split(path, "/")
	return parts[0], strings.Join(parts[1:], "/")
}
