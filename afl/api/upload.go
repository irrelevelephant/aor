package api

import "strconv"

// Protocol used by POST /api/v1/afl/exec-upload: the client submits command
// and args as form fields plus file parts; arg values that reference uploads
// use one of the placeholder prefixes below, and the server materializes the
// referenced file(s) to temp paths before dispatching.
const (
	ExecUploadPath = "/api/v1/afl/exec-upload"

	CommandField = "command"
	ArgsField    = "args"

	UploadFilePrefix = "@upload:"
	UploadDirPrefix  = "@upload-dir:"
)

// UploadFilePart returns the multipart part name for a single-file upload
// whose placeholder in args is UploadFilePrefix+key.
func UploadFilePart(key string) string { return "upload_" + key }

// UploadDirPart returns the multipart part name for the idx-th file of a
// directory upload whose placeholder is UploadDirPrefix+key.
func UploadDirPart(key string, idx int) string {
	return "upload_" + key + "_" + strconv.Itoa(idx)
}

// UploadDirPartPrefix returns the shared prefix for all part names belonging
// to a directory upload — used server-side to enumerate them.
func UploadDirPartPrefix(key string) string { return "upload_" + key + "_" }
