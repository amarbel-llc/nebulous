package tools

import (
	"context"
	"encoding/json"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/friedenberg/nebulous/internal/newsblur"
)

func registerFolderCommands(app *command.App, client *newsblur.Client) {
	mutationAnnotations := &protocol.ToolAnnotations{
		ReadOnlyHint:    protocol.BoolPtr(false),
		DestructiveHint: protocol.BoolPtr(false),
		IdempotentHint:  protocol.BoolPtr(false),
		OpenWorldHint:   protocol.BoolPtr(true),
	}

	app.AddCommand(&command.Command{
		Name: "folder_create",
		Description: command.Description{
			Short: "Create a new folder",
		},
		Annotations: mutationAnnotations,
		Params: []command.Param{
			{Name: "folder_name", Type: command.String, Required: true, Description: "Name of the folder to create"},
			{Name: "parent_folder", Type: command.String, Description: "Parent folder to nest the new folder under"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				FolderName   string `json:"folder_name"`
				ParentFolder string `json:"parent_folder"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.CreateFolder(ctx, p.FolderName, p.ParentFolder)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "folder_rename",
		Description: command.Description{
			Short: "Rename a folder",
		},
		Annotations: mutationAnnotations,
		Params: []command.Param{
			{Name: "folder_name", Type: command.String, Required: true, Description: "Current folder name"},
			{Name: "new_folder_name", Type: command.String, Required: true, Description: "New folder name"},
			{Name: "in_folder", Type: command.String, Description: "Parent folder containing the folder to rename"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				FolderName    string `json:"folder_name"`
				NewFolderName string `json:"new_folder_name"`
				InFolder      string `json:"in_folder"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.RenameFolder(ctx, p.FolderName, p.NewFolderName, p.InFolder)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "folder_delete",
		Description: command.Description{
			Short: "Delete a folder",
		},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    protocol.BoolPtr(false),
			DestructiveHint: protocol.BoolPtr(true),
			IdempotentHint:  protocol.BoolPtr(false),
			OpenWorldHint:   protocol.BoolPtr(true),
		},
		Params: []command.Param{
			{Name: "folder_name", Type: command.String, Required: true, Description: "Name of the folder to delete"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				FolderName string `json:"folder_name"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.DeleteFolder(ctx, p.FolderName)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "move_feed",
		Description: command.Description{
			Short: "Move a feed to a different folder",
		},
		Annotations: mutationAnnotations,
		Params: []command.Param{
			{Name: "feed_id", Type: command.Int, Required: true, Description: "Feed ID to move"},
			{Name: "in_folder", Type: command.String, Required: true, Description: "Current folder containing the feed"},
			{Name: "to_folder", Type: command.String, Required: true, Description: "Destination folder"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				FeedID   int    `json:"feed_id"`
				InFolder string `json:"in_folder"`
				ToFolder string `json:"to_folder"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.MoveFeed(ctx, p.FeedID, p.InFolder, p.ToFolder)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name: "move_folder",
		Description: command.Description{
			Short: "Move a folder to a different parent folder",
		},
		Annotations: mutationAnnotations,
		Params: []command.Param{
			{Name: "folder_name", Type: command.String, Required: true, Description: "Folder name to move"},
			{Name: "in_folder", Type: command.String, Required: true, Description: "Current parent folder"},
			{Name: "to_folder", Type: command.String, Required: true, Description: "Destination parent folder"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p struct {
				FolderName string `json:"folder_name"`
				InFolder   string `json:"in_folder"`
				ToFolder   string `json:"to_folder"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return command.TextErrorResult("invalid arguments: " + err.Error()), nil
			}
			result, err := client.MoveFolder(ctx, p.FolderName, p.InFolder, p.ToFolder)
			if err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			return command.TextResult(string(result)), nil
		},
	})
}
