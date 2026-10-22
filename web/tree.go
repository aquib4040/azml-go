package web

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"azml-go/helper/downloader"
	"azml-go/helper/ext"
)

type TreeNode struct {
	Name     string
	IsDir    bool
	FileID   string
	Size     int64
	Selected bool
	Children []*TreeNode
}

func BuildGDriveHTMLTree(files []downloader.GDriveFile) string {
	root := &TreeNode{Name: "Google Drive", IsDir: true}
	folderMap := map[string]*TreeNode{"": root}

	// Sort folders by depth
	folders := make([]downloader.GDriveFile, 0)
	for _, f := range files {
		if f.IsDir {
			folders = append(folders, f)
		}
	}
	sort.Slice(folders, func(i, j int) bool {
		return strings.Count(folders[i].Path, "/") < strings.Count(folders[j].Path, "/")
	})

	for _, f := range folders {
		fullPath := filepath.ToSlash(filepath.Join(f.Path, f.Name))
		parentPath := filepath.ToSlash(f.Path)
		parent := folderMap[parentPath]
		if parent == nil {
			parent = root
		}
		node := &TreeNode{Name: f.Name, IsDir: true, FileID: f.ID}
		parent.Children = append(parent.Children, node)
		folderMap[fullPath] = node
	}

	for _, f := range files {
		if f.IsDir {
			continue
		}
		parentPath := filepath.ToSlash(f.Path)
		parent := folderMap[parentPath]
		if parent == nil {
			parent = root
		}
		node := &TreeNode{
			Name:     f.Name,
			IsDir:    false,
			FileID:   f.ID,
			Size:     f.Size,
			Selected: f.Selected,
		}
		parent.Children = append(parent.Children, node)
	}

	var sb strings.Builder
	renderHTMLTree(root, &sb, 0, 0)
	return sb.String()
}

func BuildMegaHTMLTree(files []downloader.MegaFileInfo) string {
	root := &TreeNode{Name: "MEGA", IsDir: true}
	folderMap := map[string]*TreeNode{"": root}

	folders := make([]downloader.MegaFileInfo, 0)
	for _, f := range files {
		if f.IsDir {
			folders = append(folders, f)
		}
	}
	sort.Slice(folders, func(i, j int) bool {
		return strings.Count(folders[i].Path, "/") < strings.Count(folders[j].Path, "/")
	})

	for _, f := range folders {
		fullPath := filepath.ToSlash(filepath.Join(f.Path, f.Name))
		parentPath := filepath.ToSlash(f.Path)
		parent := folderMap[parentPath]
		if parent == nil {
			parent = root
		}
		node := &TreeNode{Name: f.Name, IsDir: true, FileID: f.ID}
		parent.Children = append(parent.Children, node)
		folderMap[fullPath] = node
	}

	for _, f := range files {
		if f.IsDir {
			continue
		}
		parentPath := filepath.ToSlash(f.Path)
		parent := folderMap[parentPath]
		if parent == nil {
			parent = root
		}
		node := &TreeNode{
			Name:     f.Name,
			IsDir:    false,
			FileID:   f.ID,
			Size:     f.Size,
			Selected: f.Selected,
		}
		parent.Children = append(parent.Children, node)
	}

	var sb strings.Builder
	renderHTMLTree(root, &sb, 0, 0)
	return sb.String()
}

func BuildTorrentHTMLTree(files []downloader.Aria2File) string {
	root := &TreeNode{Name: "Torrent", IsDir: true}
	for _, f := range files {
		cleanPath := filepath.ToSlash(f.Path)
		parts := strings.Split(cleanPath, "/")
		curr := root
		for i := 0; i < len(parts)-1; i++ {
			p := parts[i]
			var found *TreeNode
			for _, ch := range curr.Children {
				if ch.IsDir && ch.Name == p {
					found = ch
					break
				}
			}
			if found == nil {
				found = &TreeNode{Name: p, IsDir: true}
				curr.Children = append(curr.Children, found)
			}
			curr = found
		}
		fileName := parts[len(parts)-1]
		selected := f.Selected != "false"
		var size int64
		fmt.Sscanf(f.Length, "%d", &size)
		curr.Children = append(curr.Children, &TreeNode{
			Name:     fileName,
			IsDir:    false,
			FileID:   f.Index,
			Size:     size,
			Selected: selected,
		})
	}

	var sb strings.Builder
	renderHTMLTree(root, &sb, 0, 0)
	return sb.String()
}

func renderHTMLTree(node *TreeNode, sb *strings.Builder, depth int, folderCounter int) int {
	if depth > 0 && node.IsDir {
		sb.WriteString(`<div class="ml-2 sm:ml-4 md:ml-6 pl-1.5 sm:pl-2 md:pl-3 border-l border-outline-variant/30 flex flex-col gap-1 mt-1">`)
	}

	for _, child := range node.Children {
		if child.IsDir {
			sb.WriteString(`<div class="flex flex-col mt-2 mb-1">`)
			folderCounter++
			sb.WriteString(fmt.Sprintf(`<div class="file-row flex items-center justify-between px-2 sm:px-3 py-2 rounded-lg cursor-pointer transition-colors hover:bg-white/5 group/row">
    <div class="flex items-center gap-2 sm:gap-3 min-w-0 flex-1">
        <input class="neon-checkbox folder-checkbox shrink-0" type="checkbox" name="foldernode_%d">
        <span class="material-symbols-outlined text-secondary-dim group-hover/row:text-secondary shrink-0 transition-colors" style="font-variation-settings: 'FILL' 1;">folder_open</span>
        <span class="font-body text-xs sm:text-sm text-on-surface font-medium truncate" title="%s">%s</span>
    </div>
</div>`, folderCounter, child.Name, child.Name))
			folderCounter = renderHTMLTree(child, sb, depth+1, folderCounter)
			sb.WriteString(`</div>`)
		} else {
			checked := ""
			if child.Selected {
				checked = "checked"
			}
			sizeStr := ext.GetReadableFileSize(child.Size)
			sb.WriteString(fmt.Sprintf(`<div class="file-row flex items-center justify-between gap-2 px-2 sm:px-3 py-1.5 rounded-lg cursor-pointer transition-colors hover:bg-white/5">
    <div class="flex items-center gap-2 sm:gap-3 min-w-0 flex-1">
        <input %s class="neon-checkbox shrink-0" type="checkbox" name="filenode_%s" data-size="%d">
        <span class="material-symbols-outlined text-on-surface-variant text-base shrink-0" style="font-variation-settings: 'FILL' 0;">movie</span>
        <span class="font-body text-xs sm:text-sm text-on-surface/90 font-normal truncate" data-size="%d" title="%s">%s</span>
    </div>
    <span class="font-body text-[11px] sm:text-xs text-on-surface-variant shrink-0 font-mono whitespace-nowrap">%s</span>
    <input type="hidden" value="off" name="filenode_%s">
</div>`, checked, child.FileID, child.Size, child.Size, child.Name, child.Name, sizeStr, child.FileID))
		}
	}

	if depth > 0 && node.IsDir {
		sb.WriteString(`</div>`)
	}
	return folderCounter
}
