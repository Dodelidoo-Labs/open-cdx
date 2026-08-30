package usagehistory

import "testing"

func TestPreviewSeparatesRoutedAndNativeRequests(t *testing.T) {
	snapshot := Snapshot{
		FilesScanned: 3, EventsImported: 12, DuplicateEvents: 2, MalformedLines: 1,
		Rows: []Row{
			{Routing: RoutingRouted, Requests: 5},
			{Routing: RoutingNative, Requests: 7},
		},
	}

	preview := Preview(snapshot)
	if preview.FilesScanned != 3 || preview.EventsImported != 12 || preview.RowsFound != 2 ||
		preview.RoutedRequests != 5 || preview.NativeRequests != 7 ||
		preview.DuplicateEvents != 2 || preview.MalformedLines != 1 {
		t.Fatalf("preview = %#v", preview)
	}
}
