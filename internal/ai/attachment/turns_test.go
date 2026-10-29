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

func TestForModel_KeepsOnlyTheLatestImages(t *testing.T) {
	var turns []domain.Turn
	for id := int64(1); id <= maxImagesPerRequest+3; id++ {
		turns = append(turns, imageTurn(id))
	}

	got := ForModel(domain.Model{Vision: true}, turns)

	ids := sentImageIDs(got)
	if len(ids) != maxImagesPerRequest || ids[0] != 4 || ids[len(ids)-1] != maxImagesPerRequest+3 {
		t.Fatalf("sent images %v, want the latest %d", ids, maxImagesPerRequest)
	}
	if !strings.Contains(got[0].Text(), "Earlier image not shown") {
		t.Fatalf("unexpected placeholder: %q", got[0].Text())
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
