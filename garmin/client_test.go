package garmin

import (
	"errors"
	"net/http"
	"testing"
)

func TestParseUploadResult(t *testing.T) {
	t.Parallel()

	if err := parseUploadResult(http.StatusConflict, nil); !errors.Is(err, ErrDuplicate) {
		t.Errorf("409: got %v, want ErrDuplicate", err)
	}

	body := []byte(`{"detailedImportResult":{"failures":[{"messages":["duplicate in another sense"]}]}}`)
	if err := parseUploadResult(http.StatusOK, body); err == nil || errors.Is(err, ErrDuplicate) {
		t.Errorf("failures: got %v, want a non-duplicate error", err)
	}

	if err := parseUploadResult(http.StatusCreated, []byte(`{"detailedImportResult":{"failures":[]}}`)); err != nil {
		t.Errorf("success: got %v, want nil", err)
	}
}
