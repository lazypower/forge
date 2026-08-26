// Copyright 2026 The Forge Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"bytes"
	"net/http"
	"path"

	"gitea.dev/models/renderhelper"
	"gitea.dev/modules/charset"
	"gitea.dev/modules/git"
	"gitea.dev/modules/markup"
	"gitea.dev/modules/markup/markdown"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/templates"
	"gitea.dev/modules/typesniffer"
	"gitea.dev/services/context"
	git_service "gitea.dev/services/git"
	"gitea.dev/services/gitdiff"
)

const tplPullMarkdownPreview templates.TplName = "repo/diff/markdown_preview"

func pullContainsCommit(compareInfo *git_service.CompareInfo, commitID string) bool {
	if commitID == compareInfo.CompareBase || commitID == compareInfo.HeadCommitID {
		return true
	}
	for _, commit := range compareInfo.Commits {
		if commit != nil && commit.ID.String() == commitID {
			return true
		}
	}
	return false
}

func findPullDiffTreeRecord(diffTree *gitdiff.DiffTree, oldPath, newPath string) *gitdiff.DiffTreeRecord {
	for _, file := range diffTree.Files {
		if file.BasePath == oldPath && file.HeadPath == newPath {
			return file
		}
	}
	return nil
}

// RenderPullMarkdownPreview renders the complete Markdown file represented by a pull request diff entry.
func RenderPullMarkdownPreview(ctx *context.Context) {
	issue, ok := getPullInfo(ctx)
	if !ok {
		return
	}

	viewInfo := newPullRequestViewInfo()
	viewInfo.prepareViewInfo(ctx, issue)
	if ctx.Written() || viewInfo.CompareInfo.HeadCommitID == "" {
		return
	}

	beforeCommitID := ctx.FormString("before")
	afterCommitID := ctx.FormString("after")
	oldPath := ctx.FormString("old-path")
	newPath := ctx.FormString("new-path")
	if oldPath == "" || newPath == "" || !pullContainsCommit(&viewInfo.CompareInfo, beforeCommitID) || !pullContainsCommit(&viewInfo.CompareInfo, afterCommitID) {
		ctx.NotFound(nil)
		return
	}

	diffTree, err := gitdiff.GetDiffTree(ctx, ctx.Repo.GitRepo, false, beforeCommitID, afterCommitID)
	if err != nil {
		ctx.ServerError("GetDiffTree", err)
		return
	}
	diffFile := findPullDiffTreeRecord(diffTree, oldPath, newPath)
	if diffFile == nil {
		ctx.NotFound(nil)
		return
	}

	commitID, treePath, blobID := afterCommitID, diffFile.HeadPath, diffFile.HeadBlobID
	if diffFile.Status == "deleted" {
		commitID, treePath, blobID = beforeCommitID, diffFile.BasePath, diffFile.BaseBlobID
	}
	renderer := markup.DetectRendererTypeByFilename(treePath)
	if renderer == nil || renderer.Name() != markdown.MarkupName {
		ctx.NotFound(nil)
		return
	}

	blob, err := ctx.Repo.GitRepo.GetBlob(blobID)
	if err != nil {
		if git.IsErrNotExist(err) {
			ctx.NotFound(err)
		} else {
			ctx.ServerError("GetBlob", err)
		}
		return
	}
	if blob.Size() >= setting.UI.MaxDisplayFileSize {
		ctx.NotFound(nil)
		return
	}

	content, err := blob.GetBlobBytes(setting.UI.MaxDisplayFileSize)
	if err != nil {
		ctx.ServerError("GetBlobBytes", err)
		return
	}
	if !typesniffer.DetectContentType(content).IsRepresentableAsText() {
		ctx.NotFound(nil)
		return
	}

	rctx := renderhelper.NewRenderContextRepoFile(ctx, ctx.Repo.Repository, renderhelper.RepoFileOptions{
		CurrentRefSubURL: "commit/" + commitID,
		CurrentTreePath:  path.Dir(treePath),
	}).WithRelativePath(treePath).WithEnableHeadingIDGeneration(false)
	_, rendered, err := markupRenderToHTML(ctx, rctx, renderer, charset.ToUTF8WithFallbackReader(bytes.NewReader(content), charset.ConvertOpts{}))
	if err != nil {
		ctx.ServerError("RenderMarkdown", err)
		return
	}

	ctx.Data["RenderedMarkdown"] = rendered
	ctx.HTML(http.StatusOK, tplPullMarkdownPreview)
}
