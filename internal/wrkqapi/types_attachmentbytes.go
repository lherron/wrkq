package wrkqapi

import "os"

// attachmentUpload is the staged state for one chunked byte upload.
type attachmentUpload struct {
	taskUUID    string
	filename    string
	mimeType    string
	actor       string
	idemKey     string
	tmpPath     string
	file        *os.File
	nextSeq     int
	received    int64
	maxBytes    int64
	idemReqHash string
}
