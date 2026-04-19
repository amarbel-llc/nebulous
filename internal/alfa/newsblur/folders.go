package newsblur

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

func (c *Client) CreateFolder(ctx context.Context, folderName, parentFolder string) (json.RawMessage, error) {
	form := url.Values{"folder": {folderName}}
	if parentFolder != "" {
		form.Set("parent_folder", parentFolder)
	}
	return c.post(ctx, "/reader/add_folder", form)
}

func (c *Client) RenameFolder(ctx context.Context, folderName, newFolderName, inFolder string) (json.RawMessage, error) {
	form := url.Values{
		"folder_name":     {folderName},
		"new_folder_name": {newFolderName},
	}
	if inFolder != "" {
		form.Set("in_folder", inFolder)
	}
	return c.post(ctx, "/reader/rename_folder", form)
}

func (c *Client) DeleteFolder(ctx context.Context, folderName string) (json.RawMessage, error) {
	form := url.Values{"folder_name": {folderName}}
	return c.post(ctx, "/reader/delete_folder", form)
}

func (c *Client) MoveFeed(ctx context.Context, feedID int, inFolder, toFolder string) (json.RawMessage, error) {
	form := url.Values{
		"feed_id":   {fmt.Sprintf("%d", feedID)},
		"in_folder": {inFolder},
		"to_folder": {toFolder},
	}
	return c.post(ctx, "/reader/move_feed_to_folder", form)
}

func (c *Client) MoveFolder(ctx context.Context, folderName, inFolder, toFolder string) (json.RawMessage, error) {
	form := url.Values{
		"folder_name": {folderName},
		"in_folder":   {inFolder},
		"to_folder":   {toFolder},
	}
	return c.post(ctx, "/reader/move_folder_to_folder", form)
}
