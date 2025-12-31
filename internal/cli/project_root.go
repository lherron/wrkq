package cli

import (
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/paths"
)

func normalizeProjectRoot(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	root := strings.TrimSpace(cfg.ProjectRoot)
	root = strings.Trim(root, "/")
	return root
}

func applyProjectRootToPath(cfg *config.Config, path string, defaultToRoot bool) string {
	root := normalizeProjectRoot(cfg)
	trimmed := strings.TrimSpace(path)
	if root == "" {
		return trimmed
	}
	if trimmed == "" {
		if defaultToRoot {
			return root
		}
		return trimmed
	}
	return applyProjectRootToken(root, trimmed)
}

func applyProjectRootToSelector(cfg *config.Config, selector string, defaultToRoot bool) string {
	root := normalizeProjectRoot(cfg)
	trimmed := strings.TrimSpace(selector)
	if root == "" {
		return trimmed
	}
	if trimmed == "" {
		if defaultToRoot {
			return root
		}
		return trimmed
	}

	if strings.HasPrefix(trimmed, "t:") || strings.HasPrefix(trimmed, "c:") {
		prefix := trimmed[:2]
		token := strings.TrimSpace(trimmed[2:])
		if token == "" {
			if defaultToRoot {
				return prefix + root
			}
			return trimmed
		}
		return prefix + applyProjectRootToken(root, token)
	}

	return applyProjectRootToken(root, trimmed)
}

func applyProjectRootToPaths(cfg *config.Config, pathsIn []string, defaultToRoot bool) []string {
	if len(pathsIn) == 0 {
		root := normalizeProjectRoot(cfg)
		if defaultToRoot && root != "" {
			return []string{root}
		}
		return pathsIn
	}
	out := make([]string, 0, len(pathsIn))
	for _, path := range pathsIn {
		out = append(out, applyProjectRootToPath(cfg, path, false))
	}
	return out
}

func applyProjectRootToken(root, token string) string {
	if token == "" {
		return token
	}

	if id.IsFriendlyID(token) || id.IsUUID(token) {
		return token
	}

	normalized := strings.Trim(token, "/")
	if normalized == "" {
		return normalized
	}
	if normalized == root || strings.HasPrefix(normalized, root+"/") {
		return normalized
	}
	return paths.JoinPath(root, normalized)
}
