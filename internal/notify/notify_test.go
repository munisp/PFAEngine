package notify

import (
	"context"
	"reflect"
	"testing"
)

func TestWhatsAppDispatchHook(t *testing.T) {
	var gotTo, gotTemplate string
	var gotParams []string
	svc := NewService(&MockSender{})
	svc.SendWhatsApp = func(ctx context.Context, to, template string, params []string) error {
		gotTo, gotTemplate, gotParams = to, template, params
		return nil
	}
	svc.RegisterTemplate(Template{ID: "liveness-reminder", Channel: WhatsApp, Name: "liveness_reminder", Body: "Dear {{name}}, due {{date}}.", Version: 1})

	rec, err := svc.Send("t1", "liveness-reminder", "2348030000000", map[string]string{"name": "Ada", "date": "2025-06-30"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotTo != "2348030000000" || gotTemplate != "liveness_reminder" {
		t.Errorf("hook got to=%q template=%q", gotTo, gotTemplate)
	}
	// Params are ordered by sorted var keys: date, name.
	if want := []string{"2025-06-30", "Ada"}; !reflect.DeepEqual(gotParams, want) {
		t.Errorf("params = %v, want %v", gotParams, want)
	}
	if rec.Channel != WhatsApp || rec.DeliveryID != "whatsapp:liveness_reminder" {
		t.Errorf("record = %+v", rec)
	}
}

func TestWhatsAppFallbackToGenericSender(t *testing.T) {
	mock := &MockSender{}
	svc := NewService(mock) // no SendWhatsApp hook wired
	svc.RegisterTemplate(Template{ID: "liveness-reminder", Channel: WhatsApp, Name: "liveness_reminder", Body: "Dear {{name}}.", Version: 1})
	if _, err := svc.Send("t1", "liveness-reminder", "2348030000000", map[string]string{"name": "Ada"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(mock.Delivered) != 1 || mock.Delivered[0] != "WHATSAPP|2348030000000|Dear Ada." {
		t.Errorf("delivered = %v", mock.Delivered)
	}
}
