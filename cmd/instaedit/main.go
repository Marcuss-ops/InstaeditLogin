package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "media":
		err = runMedia(os.Args[2:])
	case "youtube":
		err = runYouTube(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "instaedit:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  instaedit media upload <path>
  instaedit youtube editor-session create --workspace <id> --account <id> --video <youtube-video-id>
  instaedit youtube editor-session get --session <id>
  instaedit youtube upload --workspace <id> --account <id> --file <video> [--cover <image>]
  instaedit youtube thumbnail set --session <id> --file <path>
  instaedit youtube publish --session <id> [--privacy <status>] [--title <t>] [--description <d>]
  instaedit youtube cover-and-publish --session <id> --cover <path> [--privacy <status>] [--title <t>] [--description <d>]
`)
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func runMedia(args []string) error {
	if len(args) < 2 || args[0] != "upload" {
		return fmt.Errorf("usage: instaedit media upload <path>")
	}
	if err := validateNoExtraArgs("media upload", args[2:]); err != nil {
		return err
	}
	if err := validateRequiredPath("<path>", args[1]); err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	mediaID, err := uploadFile(c, args[1])
	if err != nil {
		return err
	}
	return printJSON(map[string]string{"media_id": mediaID})
}

func runYouTube(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: instaedit youtube <command>")
	}
	switch args[0] {
	case "editor-session":
		return runEditorSession(args[1:])
	case "upload":
		return runYouTubeUpload(args[1:])
	case "thumbnail":
		return runThumbnail(args[1:])
	case "publish":
		return runPublish(args[1:])
	case "cover-and-publish":
		return runCoverAndPublish(args[1:])
	default:
		return fmt.Errorf("unknown youtube command %q", args[0])
	}
}

func runEditorSession(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: instaedit youtube editor-session create|\u001b[?25lget ...")
	}
	if args[0] == "get" {
		return runEditorSessionGet(args[1:])
	}
	if args[0] != "create" {
		return fmt.Errorf("usage: instaedit youtube editor-session create --workspace <id> --account <id> --video <id>")
	}
	fs := flag.NewFlagSet("editor-session create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	workspace := fs.Int64("workspace", 0, "workspace id")
	account := fs.Int64("account", 0, "platform account id")
	video := fs.String("video", "", "youtube video id")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if err := validateNoExtraArgs("youtube editor-session create", fs.Args()); err != nil {
		return err
	}
	if *workspace <= 0 || *account <= 0 || *video == "" {
		return fmt.Errorf("--workspace, --account and --video are required")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	session, err := createEditorSession(c, *workspace, *account, *video)
	if err != nil {
		return err
	}
	return printJSON(session)
}

func runEditorSessionGet(args []string) error {
	fs := flag.NewFlagSet("editor-session get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	session := fs.String("session", "", "editor session id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateNoExtraArgs("youtube editor-session get", fs.Args()); err != nil {
		return err
	}
	if *session == "" {
		return fmt.Errorf("--session is required")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	response, err := getEditorSession(c, *session)
	if err != nil {
		return err
	}
	return printJSON(response)
}

func runYouTubeUpload(args []string) error {
	fs := flag.NewFlagSet("youtube upload", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	workspace := fs.Int64("workspace", 0, "workspace id")
	account := fs.Int64("account", 0, "YouTube platform account id")
	video := fs.String("file", "", "local video path")
	cover := fs.String("cover", "", "optional cover image path")
	title := fs.String("title", "", "YouTube title")
	description := fs.String("description", "", "YouTube description")
	privacy := fs.String("privacy", "public", "final privacy status (public/unlisted/private)")
	pollTimeout := fs.Duration("timeout", 30*time.Minute, "maximum time to wait for the YouTube video id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateNoExtraArgs("youtube upload", fs.Args()); err != nil {
		return err
	}
	if *workspace <= 0 || *account <= 0 {
		return fmt.Errorf("--workspace and --account are required")
	}
	if err := validateRequiredPath("--file", *video); err != nil {
		return err
	}
	if *cover != "" {
		if err := validateRequiredPath("--cover", *cover); err != nil {
			return err
		}
	}
	if err := validatePrivacyStatus(*privacy); err != nil {
		return err
	}
	if *pollTimeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	result, err := uploadAndPublishYouTube(
		c,
		*workspace,
		*account,
		*video,
		*cover,
		*title,
		*description,
		*privacy,
		*pollTimeout,
	)
	if err != nil {
		return err
	}
	return printJSON(result)
}

func runThumbnail(args []string) error {
	if len(args) < 1 || args[0] != "set" {
		return fmt.Errorf("usage: instaedit youtube thumbnail set --session <id> --file <path>")
	}
	fs := flag.NewFlagSet("thumbnail set", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	session := fs.String("session", "", "editor session id")
	file := fs.String("file", "", "cover image path")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if err := validateNoExtraArgs("youtube thumbnail set", fs.Args()); err != nil {
		return err
	}
	if err := validateRequiredPath("--file", *file); err != nil {
		return err
	}
	if *session == "" {
		return fmt.Errorf("--session is required")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	mediaID, err := setThumbnailFromFile(c, *session, *file)
	if err != nil {
		return err
	}
	return printJSON(map[string]string{"session_id": *session, "thumbnail_media_id": mediaID})
}

func runPublish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	session := fs.String("session", "", "editor session id")
	privacy := fs.String("privacy", "public", "privacy status (public/unlisted/private)")
	title := fs.String("title", "", "optional title override")
	description := fs.String("description", "", "optional description override")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateNoExtraArgs("youtube publish", fs.Args()); err != nil {
		return err
	}
	if *session == "" {
		return fmt.Errorf("--session is required")
	}
	if err := validatePrivacyStatus(*privacy); err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	resp, err := publishVideo(c, *session, *privacy, *title, *description)
	if err != nil {
		return err
	}
	return printJSON(resp)
}

func runCoverAndPublish(args []string) error {
	fs := flag.NewFlagSet("cover-and-publish", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	session := fs.String("session", "", "editor session id")
	cover := fs.String("cover", "", "cover image path")
	privacy := fs.String("privacy", "public", "privacy status (public/unlisted/private)")
	title := fs.String("title", "", "optional title override")
	description := fs.String("description", "", "optional description override")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateNoExtraArgs("youtube cover-and-publish", fs.Args()); err != nil {
		return err
	}
	if *session == "" {
		return fmt.Errorf("--session is required")
	}
	if err := validateRequiredPath("--cover", *cover); err != nil {
		return err
	}
	if err := validatePrivacyStatus(*privacy); err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	resp, err := coverAndPublish(c, *session, *cover, *privacy, *title, *description)
	if err != nil {
		return err
	}
	return printJSON(resp)
}
