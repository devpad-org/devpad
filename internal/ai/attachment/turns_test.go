package attachment

import (
	"fmt"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

func loadedImage(id int64) domain.ImagePart {
	return domain.ImagePart{AttachmentID: id, MediaType: "image/png", Width: 10, Height: 10, Data: []byte(fmt.Sprintf("image-%d", id))}
}

func imageTurn(ids ...int64) domain.Turn {
	turn := domain.Turn{Role: domain.RoleUser}
	for _, id := range ids {
		turn.Parts = append(turn.Parts, domain.NewImagePart(loadedImage(id)))
	}
	return turn
}

func sentImageIDs(turns []domain.Turn) []int64 {
	var ids []int64
	for _, turn := range turns {
		for _, part := range turn.Parts {
			if part.Kind == domain.PartImage {
				ids = append(ids, part.Image.AttachmentID)
			}
			if part.Kind == domain.PartToolResult {
				for _, image := range part.ToolResult.Images {
					ids = append(ids, image.AttachmentID)
				}
			}
		}
	}
	return ids
}

func TestForModel_ReplacesImagesForModelsWithoutVision(t *testing.T) {
	turns := []domain.Turn{
		imageTurn(1),
		domain.NewToolResultTurnFromPart(domain.ToolResultPart{ToolCallID: "c", Name: "read_file", Content: "Image a.png", Images: []domain.ImagePart{loadedImage(2)}}),
	}

	got := ForModel(domain.Model{ID: "text-only", Name: "Text Only"}, turns)

	if ids := sentImageIDs(got); len(ids) != 0 {
		t.Fatalf("sent images %v to a model without vision", ids)
	}
	if got[0].Parts[0].Kind != domain.PartText || !strings.Contains(got[0].Parts[0].Text, "Text Only can't view images") {
		t.Fatalf("unexpected placeholder: %+v", got[0].Parts[0])
	}
	if content := got[1].ToolResult().Content; !strings.HasPrefix(content, "Image a.png\n") || !strings.Contains(content, "can't view images") {
		t.Fatalf("unexpected tool result content: %q", content)
	}
	if turns[0].Parts[0].Kind != domain.PartImage || len(turns[1].ToolResult().Images) != 1 {
		t.Fatal("expected the input turns to be left unchanged")
	}
}

func TestForModel_DropsTheOldestImagesInSteps(t *testing.T) {
	var turns []domain.Turn
	for id := int64(1); id <= maxImagesPerRequest+3; id++ {
		turns = append(turns, imageTurn(id))
	}

	got := ForModel(domain.Model{Vision: true}, turns)

	// Three images too many drop a whole step of the oldest.
	ids := sentImageIDs(got)
	if len(ids) != maxImagesPerRequest+3-dropStep || ids[0] != dropStep+1 || ids[len(ids)-1] != maxImagesPerRequest+3 {
		t.Fatalf("sent images %v, want %d to %d", ids, dropStep+1, maxImagesPerRequest+3)
	}
	if !strings.Contains(got[0].Text(), "Earlier image not shown") {
		t.Fatalf("unexpected placeholder: %q", got[0].Text())
	}
}

func TestForModel_KeepsTheOldestImageSentForAStep(t *testing.T) {
	// Requests keep the same start, which providers cache, until another
	// step of images is added.
	var turns []domain.Turn
	oldest := map[int64]bool{}
	for id := int64(1); id <= 3*maxImagesPerRequest; id++ {
		turns = append(turns, imageTurn(id))
		if ids := sentImageIDs(ForModel(domain.Model{Vision: true}, turns)); len(ids) > 0 {
			oldest[ids[0]] = true
		}
	}
	if len(oldest) != 1+(3*maxImagesPerRequest-maxImagesPerRequest+dropStep-1)/dropStep {
		t.Fatalf("oldest images sent %v, want one per step", oldest)
	}
}
func TestForModel_KeepsOnlyTheLatestImagesThatFitInARequest(t *testing.T) {
	big := func(id int64) domain.Turn {
		image := loadedImage(id)
		image.Data = make([]byte, 3<<20)
		return domain.Turn{Role: domain.RoleUser, Parts: []domain.Part{domain.NewImagePart(image)}}
	}
	// Five 3 MB images and a small one fit in 16 MB, the next 3 MB one
	// doesn't, and once one doesn't, no older one is sent: not even the
	// small first image, which would fit.
	turns := []domain.Turn{imageTurn(1), big(2), imageTurn(3)}
	for id := int64(4); id <= 8; id++ {
		turns = append(turns, big(id))
	}

	got := ForModel(domain.Model{Vision: true}, turns)

	if ids := sentImageIDs(got); fmt.Sprint(ids) != "[3 4 5 6 7 8]" {
		t.Fatalf("sent images %v, want [3 4 5 6 7 8]", ids)
	}
	for i := range 2 {
		if !strings.Contains(got[i].Text(), "up to 16 MB in all") {
			t.Fatalf("turn %d: unexpected placeholder %q", i, got[i].Text())
		}
	}
}

func TestForModel_KeepsToolResultImagesInOrder(t *testing.T) {
	result := domain.ToolResultPart{ToolCallID: "c", Name: "read_file", Content: "Images",
		Images: []domain.ImagePart{{AttachmentID: 1}, loadedImage(2), {AttachmentID: 3}, loadedImage(4)}}
	got := ForModel(domain.Model{Vision: true}, []domain.Turn{domain.NewToolResultTurnFromPart(result)})

	if ids := sentImageIDs(got); fmt.Sprint(ids) != "[2 4]" {
		t.Fatalf("sent images %v, want [2 4]", ids)
	}
	if content := got[0].ToolResult().Content; strings.Count(content, "no longer available") != 2 {
		t.Fatalf("unexpected content: %q", content)
	}
}

func TestForModel_SharesTurnsThatNeedNoChange(t *testing.T) {
	turns := []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hi"), imageTurn(1)}
	got := ForModel(domain.Model{Vision: true}, turns)
	if &got[0] != &turns[0] {
		t.Fatal("expected unchanged turns to be returned as they are")
	}
}

func TestForModel_ReplacesImagesWithoutData(t *testing.T) {
	turn := domain.Turn{Role: domain.RoleUser, Parts: []domain.Part{domain.NewImagePart(domain.ImagePart{AttachmentID: 9})}}
	got := ForModel(domain.Model{Vision: true}, []domain.Turn{turn})
	if got[0].Parts[0].Kind != domain.PartText || !strings.Contains(got[0].Parts[0].Text, "no longer available") {
		t.Fatalf("unexpected part: %+v", got[0].Parts[0])
	}
}

func TestForModel_LeavesImagesThatFitWhenAStepWouldLeaveTooFew(t *testing.T) {
	// Four 4 MB images fill a request, so a step would leave none.
	var turns []domain.Turn
	for id := int64(1); id <= 6; id++ {
		image := loadedImage(id)
		image.Data = make([]byte, 4<<20)
		turns = append(turns, domain.Turn{Role: domain.RoleUser, Parts: []domain.Part{domain.NewImagePart(image)}})
	}

	if ids := sentImageIDs(ForModel(domain.Model{Vision: true}, turns)); fmt.Sprint(ids) != "[3 4 5 6]" {
		t.Fatalf("sent images %v, want [3 4 5 6]", ids)
	}
}

func TestForModel_KeepsNotesApartFromText(t *testing.T) {
	turn := domain.Turn{Role: domain.RoleUser, Parts: []domain.Part{
		domain.NewImagePart(domain.ImagePart{AttachmentID: 1}),
		domain.NewImagePart(domain.ImagePart{AttachmentID: 2}),
		{Kind: domain.PartText, Text: "What is this?"},
		domain.NewImagePart(domain.ImagePart{AttachmentID: 3}),
	}}
	got := ForModel(domain.Model{Vision: true}, []domain.Turn{turn})

	note := "[Image not shown: it is no longer available.]"
	want := note + "\n" + note + "\nWhat is this?\n" + note
	if text := got[0].Text(); text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
}

func TestForModel_NotesImagesOutsideTheRequestThatAreNotLoaded(t *testing.T) {
	// Images a run left unloaded, because no request sends them, are noted
	// as earlier images, not as gone.
	var turns []domain.Turn
	for id := int64(1); id <= maxImagesPerRequest+1; id++ {
		image := loadedImage(id)
		if id <= dropStep {
			image.Size, image.Data = len(image.Data), nil
		}
		turns = append(turns, domain.Turn{Role: domain.RoleUser, Parts: []domain.Part{domain.NewImagePart(image)}})
	}

	got := ForModel(domain.Model{Vision: true}, turns)
	if !strings.Contains(got[0].Text(), "Earlier image not shown") {
		t.Fatalf("unexpected placeholder: %q", got[0].Text())
	}
	if ids := sentImageIDs(got); len(ids) != maxImagesPerRequest+1-dropStep {
		t.Fatalf("sent images %v", ids)
	}
}

func TestSent_IsWhatLaterRequestsCanSend(t *testing.T) {
	// Every image a request sends as images are added was either sent by
	// the first request or added since, so a run that loads only Sent's
	// images at its start has every image it later sends.
	sizes := []int{1 << 20, 3 << 20, 512 << 10, 2 << 20, 4 << 20, 1 << 20, 3 << 20}
	var turns []domain.Turn
	for id := int64(1); id <= 40; id++ {
		image := loadedImage(id)
		image.Data = make([]byte, sizes[int(id)%len(sizes)])
		turns = append(turns, domain.Turn{Role: domain.RoleUser, Parts: []domain.Part{domain.NewImagePart(image)}})
	}
	for start := 1; start < len(turns); start++ {
		loaded := map[int64]bool{}
		for _, image := range Sent(turns[:start]) {
			loaded[image.AttachmentID] = true
		}
		for end := start; end <= len(turns); end++ {
			for _, id := range sentImageIDs(ForModel(domain.Model{Vision: true}, turns[:end])) {
				if id <= int64(start) && !loaded[id] {
					t.Fatalf("a request over %d turns sends image %d, which Sent over %d left out", end, id, start)
				}
			}
		}
	}
}
