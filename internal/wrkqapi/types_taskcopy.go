package wrkqapi

// TaskCopyParams selects ONE source task + a destination container and the copy
// options. It is the server-owned "one source-task copy envelope" (daedalus
// hrcchat#10196, T-05111): the CLI fans out over multiple sources and calls this
// once per source. Field order is the daedalus-approved semantic order.
type TaskCopyParams struct {
	Source          string `json:"source"`
	Destination     string `json:"destination"`
	Overwrite       bool   `json:"overwrite,omitempty"`
	WithAttachments bool   `json:"withAttachments,omitempty"`
	Shallow         bool   `json:"shallow,omitempty"`
	ExpectEtag      *int64 `json:"expectEtag,omitempty"`
	Actor           string `json:"actor,omitempty"`
	IdempotencyKey  string `json:"idempotencyKey,omitempty"`
}

// WrkqTaskCopyResult is the per-source copy outcome DTO. Field order matches the
// daedalus-approved semantic order; the snake_case JSON tags are deliberately the
// LEGACY copyResult output keys so the mirror renders byte-identical output.
type WrkqTaskCopyResult struct {
	SourceID          string `json:"source_id"`
	SourceUUID        string `json:"source_uuid"`
	DestID            string `json:"dest_id"`
	DestUUID          string `json:"dest_uuid"`
	DestPath          string `json:"dest_path"`
	AttachmentsCopied int    `json:"attachments_copied,omitempty"`
	WithFiles         bool   `json:"with_files,omitempty"`
}

// stagedFile is one attachment file copied into a temp staging path BEFORE the DB
// commit; on commit success it is atomically renamed into finalPath.
type stagedFile struct {
	tmpPath   string
	finalPath string
}
