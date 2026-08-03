package services

import "context"

// FakeYouTubeLiveGateway is a deterministic in-memory gateway for worker and
// service tests. Function fields override individual operations; call counts
// and the last token/IDs make it easy to assert orchestration without HTTP.
type FakeYouTubeLiveGateway struct {
	CreateStreamFn    func(context.Context, string, CreateStreamInput) (*YouTubeStream, error)
	CreateBroadcastFn func(context.Context, string, CreateBroadcastInput) (*YouTubeBroadcast, error)
	BindFn            func(context.Context, string, string, string) error
	GetStreamFn       func(context.Context, string, string) (*YouTubeStreamStatus, error)
	GetBroadcastFn    func(context.Context, string, string) (*YouTubeBroadcastStatus, error)
	TransitionFn      func(context.Context, string, string, string) error
	CompleteFn        func(context.Context, string, string) error

	CreateStreamCalls    int
	CreateBroadcastCalls int
	BindCalls            int
	GetStreamCalls       int
	GetBroadcastCalls    int
	TransitionCalls      int
	CompleteCalls        int
	LastToken            string
	LastBroadcastID      string
	LastStreamID         string
	LastTarget           string
}

func (f *FakeYouTubeLiveGateway) CreateStream(ctx context.Context, token string, input CreateStreamInput) (*YouTubeStream, error) {
	f.CreateStreamCalls++
	f.LastToken = token
	if f.CreateStreamFn != nil {
		return f.CreateStreamFn(ctx, token, input)
	}
	return &YouTubeStream{ID: "fake-stream"}, nil
}
func (f *FakeYouTubeLiveGateway) CreateBroadcast(ctx context.Context, token string, input CreateBroadcastInput) (*YouTubeBroadcast, error) {
	f.CreateBroadcastCalls++
	f.LastToken = token
	if f.CreateBroadcastFn != nil {
		return f.CreateBroadcastFn(ctx, token, input)
	}
	return &YouTubeBroadcast{ID: "fake-broadcast"}, nil
}
func (f *FakeYouTubeLiveGateway) Bind(ctx context.Context, token, broadcastID, streamID string) error {
	f.BindCalls++
	f.LastToken, f.LastBroadcastID, f.LastStreamID = token, broadcastID, streamID
	if f.BindFn != nil {
		return f.BindFn(ctx, token, broadcastID, streamID)
	}
	return nil
}
func (f *FakeYouTubeLiveGateway) GetStream(ctx context.Context, token, streamID string) (*YouTubeStreamStatus, error) {
	f.GetStreamCalls++
	f.LastToken, f.LastStreamID = token, streamID
	if f.GetStreamFn != nil {
		return f.GetStreamFn(ctx, token, streamID)
	}
	return &YouTubeStreamStatus{ID: streamID, StreamStatus: "active"}, nil
}
func (f *FakeYouTubeLiveGateway) GetBroadcast(ctx context.Context, token, broadcastID string) (*YouTubeBroadcastStatus, error) {
	f.GetBroadcastCalls++
	f.LastToken, f.LastBroadcastID = token, broadcastID
	if f.GetBroadcastFn != nil {
		return f.GetBroadcastFn(ctx, token, broadcastID)
	}
	return &YouTubeBroadcastStatus{ID: broadcastID, LifeCycleStatus: "ready"}, nil
}
func (f *FakeYouTubeLiveGateway) Transition(ctx context.Context, token, broadcastID, target string) error {
	f.TransitionCalls++
	f.LastToken, f.LastBroadcastID, f.LastTarget = token, broadcastID, target
	if f.TransitionFn != nil {
		return f.TransitionFn(ctx, token, broadcastID, target)
	}
	return nil
}
func (f *FakeYouTubeLiveGateway) Complete(ctx context.Context, token, broadcastID string) error {
	f.CompleteCalls++
	f.LastToken, f.LastBroadcastID = token, broadcastID
	if f.CompleteFn != nil {
		return f.CompleteFn(ctx, token, broadcastID)
	}
	return nil
}

var _ YouTubeLiveGateway = (*FakeYouTubeLiveGateway)(nil)
