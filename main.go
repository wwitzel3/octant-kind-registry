package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync/atomic"

	"github.com/vmware-tanzu/octant/pkg/plugin"
	"github.com/vmware-tanzu/octant/pkg/view/flexlayout"

	"github.com/vmware-tanzu/octant/pkg/action"
	"github.com/vmware-tanzu/octant/pkg/navigation"

	"github.com/vmware-tanzu/octant/pkg/plugin/service"
	"github.com/vmware-tanzu/octant/pkg/view/component"
)

var (
	pluginName   = "waynewitzel.com/kind-images"
	loadAction   = "waynewitzel.com/kind-load-image"
	deleteAction = "waynewitzel.com/kind-delete-image"
)

type imagePlugin struct {
	loading int32
}

type dockerImage struct {
	Containers   string
	CreatedAt    string
	CreatedSince string
	Digest       string
	ID           string
	Repository   string
	SharedSize   string
	Size         string
	Tag          string
	UniqueSize   string
	VirtualSize  string
}

type kindImages struct {
	Images []kindImage `json:"images"`
}

type kindImage struct {
	ID          string   `json:"id"`
	UID         string   `json:"uid"`
	RepoTags    []string `json:"repoTags"`
	RepoDigests []string `json:"repoDigests"`
	Size        string   `json:"size"`
	Username    string   `json:"username"`
}

func listKindImages() kindImages {
	cmd := exec.Command("docker", "exec", "kind-control-plane", "crictl", "images", "--output=json") //, "images", "--output json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		log.Fatalf("failed crictl: %s", err)
	}

	var images kindImages
	err = json.Unmarshal(stdout.Bytes(), &images)
	if err != nil {
		log.Fatalf("failed crictl json: %s", err)
	}

	return images
}

func listDockerImages() []dockerImage {
	cmd := exec.Command("docker", "image", "ls", "--format={{json .}}") //, "--format={{json .}}") // image ls --format={{json .}}")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		log.Fatalf("failed %s", err)
	}

	imageSlice := strings.Split(string(stdout.Bytes()), "\n")

	var images []dockerImage
	for _, i := range imageSlice {
		var image dockerImage
		err = json.Unmarshal([]byte(i), &image)
		if err != nil {
			continue
		}
		images = append(images, image)
		// fmt.Printf("%+v\n", image)
	}
	return images
}

func main() {
	// Remove the prefix from the go logger since Octant will print logs with timestamps.
	log.SetPrefix("")

	p := &imagePlugin{}

	// Tell Octant to call this plugin when printing configuration or tabs for Pods
	capabilities := &plugin.Capabilities{
		ActionNames: []string{deleteAction, loadAction},
		IsModule:    true,
	}

	// Set up what should happen when Octant calls this plugin.
	options := []service.PluginOption{
		service.WithNavigation(p.handleNav, p.initRoutes),
		service.WithActionHandler(p.handleActions),
	}

	// Use the plugin service helper to register this plugin.
	ps, err := service.Register(pluginName, "kind images plugin", capabilities, options...)
	if err != nil {
		log.Fatal(err)
	}

	// The plugin can log and the log messages will show up in Octant.
	log.Printf("docker registry plugin is starting")
	ps.Serve()
}

func (i *imagePlugin) IsLoading() bool {
	return atomic.LoadInt32(&(i.loading)) != 0
}

func (i *imagePlugin) SetLoading(b bool) {
	var j int32 = 0
	if b {
		j = 1
	}
	atomic.StoreInt32(&(i.loading), j)
}

func (i *imagePlugin) handleActions(request *service.ActionRequest) error {
	switch request.ActionName {
	case loadAction:
		if i.IsLoading() {
			return fmt.Errorf("already loading an image, please wait")
		}

		imageID, err := request.Payload.String("imageID")
		if err != nil {
			return err
		}
		return i.loadImage(imageID)
	case deleteAction:
		imageID, err := request.Payload.String("imageID")
		if err != nil {
			return err
		}
		return i.deleteImage(imageID)
	default:
		return fmt.Errorf("unhandled action")
	}
}

func (i *imagePlugin) loadImage(imageID string) error {
	i.SetLoading(true)
	defer i.SetLoading(false)

	// kind load docker-image {{imageID}}
	cmd := exec.Command("kind", "load", "docker-image", imageID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("loadImage: %w", err)
	}

	return nil
}

func (i *imagePlugin) deleteImage(imageID string) error {
	// kind load docker-image {{imageID}}
	cmd := exec.Command("docker", "exec", "kind-control-plane", "crictl", "rmi", imageID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("deleteImage: %w", err)
	}
	return nil
}

func (i *imagePlugin) handleNav(request *service.NavigationRequest) (navigation.Navigation, error) {
	return navigation.Navigation{
		Title:    "Local Images",
		Path:     request.GeneratePath(""),
		IconName: "storage",
	}, nil
}

func (i *imagePlugin) initRoutes(router *service.Router) {
	router.HandleFunc("*", i.handleOverview)
}

func (i *imagePlugin) handleOverview(request service.Request) (component.ContentResponse, error) {
	table := component.NewTable("Docker Images", "No images found",
		component.NewTableCols("Repository", "Tag", "Image ID", "Created", "Size"))

	for _, image := range listDockerImages() {
		table.Add(rowPrinter(image))
	}

	kindTable := component.NewTable("Kind Images", "No images found",
		component.NewTableCols("Image", "Image ID", "Size"))

	for _, image := range listKindImages().Images {
		for _, repoTag := range image.RepoTags {
			kindTable.Add(kindPrinter(image, repoTag))
		}
	}

	layout := flexlayout.New()

	if i.IsLoading() {
		loadingSection := layout.AddSection()
		loadingSection.Add(component.NewText("Started loading image in to kind..."), component.WidthFull)
		kindTable.SetIsLoading(true)
	} else {
		kindTable.SetIsLoading(false)
	}

	dockerSection := layout.AddSection()
	dockerSection.Add(table, component.WidthFull)

	kindSection := layout.AddSection()
	kindSection.Add(kindTable, component.WidthFull)

	flexComponent := layout.ToComponent("Local Images")
	contentResponse := component.NewContentResponse(component.TitleFromString("Local Images"))
	contentResponse.Add(flexComponent)
	return *contentResponse, nil
}

func rowPrinter(image dockerImage) component.TableRow {
	row := component.TableRow{}
	row["Repository"] = component.NewText(fmt.Sprintf("%s", image.Repository))
	row["Tag"] = component.NewText(fmt.Sprintf("%s", image.Tag))
	row["Image ID"] = component.NewText(fmt.Sprintf("%s", image.ID))
	row["Created"] = component.NewText(fmt.Sprintf("%s", image.CreatedSince))
	row["Size"] = component.NewText(fmt.Sprintf("%s", image.Size))

	action := component.GridAction{
		Name:       "Load into Kind",
		ActionPath: loadAction,
		Payload: action.Payload{
			"action":  loadAction,
			"imageID": fmt.Sprintf("%s:%s", image.Repository, image.Tag),
		},
		Type: component.GridActionPrimary,
	}
	row.AddAction(action)

	return row
}

func kindPrinter(image kindImage, repoTag string) component.TableRow {
	row := component.TableRow{}
	row["Image"] = component.NewText(fmt.Sprintf("%s", repoTag))
	row["Image ID"] = component.NewText(fmt.Sprintf("%s", image.ID))
	row["Size"] = component.NewText(fmt.Sprintf("%s", image.Size))

	confirmation := &component.Confirmation{
		Title: "Are you sure?",
		Body:  fmt.Sprintf("Do you want to delete %s from your kind images?", repoTag),
	}

	action := component.GridAction{
		Name:       "Delete",
		ActionPath: deleteAction,
		Payload: action.Payload{
			"action":  deleteAction,
			"imageID": image.ID,
		},
		Confirmation: confirmation,
		Type:         component.GridActionDanger,
	}

	row.AddAction(action)

	return row
}
