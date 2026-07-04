package reachymini

import (
	"context"
	"net/http"
)

// ListRunningMoves returns the UUIDs of moves currently in progress.
func (c *Client) ListRunningMoves(ctx context.Context) ([]MoveUUID, error) {
	var moves []MoveUUID
	err := c.doJSON(ctx, http.MethodGet, "/api/move/running", nil, &moves)
	return moves, err
}

// Goto smoothly interpolates to the given target pose(s) over req.Duration.
func (c *Client) Goto(ctx context.Context, req GotoRequest) (MoveUUID, error) {
	var uuid MoveUUID
	err := c.doJSON(ctx, http.MethodPost, "/api/move/goto", req, &uuid)
	return uuid, err
}

// WakeUp plays the robot's built-in wake-up move.
func (c *Client) WakeUp(ctx context.Context) (MoveUUID, error) {
	var uuid MoveUUID
	err := c.doJSON(ctx, http.MethodPost, "/api/move/play/wake_up", nil, &uuid)
	return uuid, err
}

// GotoSleep plays the robot's built-in sleep move.
func (c *Client) GotoSleep(ctx context.Context) (MoveUUID, error) {
	var uuid MoveUUID
	err := c.doJSON(ctx, http.MethodPost, "/api/move/play/goto_sleep", nil, &uuid)
	return uuid, err
}

// ListRecordedMoveDatasets lists the recorded moves available in datasetName.
func (c *Client) ListRecordedMoveDatasets(ctx context.Context, datasetName string) ([]string, error) {
	var names []string
	// datasetName may itself contain "/" (the daemon route uses FastAPI's
	// {dataset_name:path} converter), so it's appended raw, not escaped.
	err := c.doJSON(ctx, http.MethodGet, "/api/move/recorded-move-datasets/list/"+datasetName, nil, &names)
	return names, err
}

// PlayRecordedMoveDataset plays moveName from datasetName.
func (c *Client) PlayRecordedMoveDataset(ctx context.Context, datasetName, moveName string) (MoveUUID, error) {
	var uuid MoveUUID
	path := "/api/move/play/recorded-move-dataset/" + datasetName + "/" + moveName
	err := c.doJSON(ctx, http.MethodPost, path, nil, &uuid)
	return uuid, err
}

// Stop halts the move identified by uuid (as returned by Goto, WakeUp,
// GotoSleep, or PlayRecordedMoveDataset; see also ListRunningMoves).
func (c *Client) Stop(ctx context.Context, uuid MoveUUID) error {
	return c.doJSON(ctx, http.MethodPost, "/api/move/stop", uuid, nil)
}

// SetTarget directly commands a target pose with no interpolation (lower
// latency than Goto, useful for teleop). For continuous streaming control,
// use StreamSetTarget instead.
func (c *Client) SetTarget(ctx context.Context, target FullBodyTarget) error {
	return c.doJSON(ctx, http.MethodPost, "/api/move/set_target", target, nil)
}
