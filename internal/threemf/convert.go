package threemf

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// ErrNotSlicerProject is returned by mutations of a package no slicer saved
// (no Metadata/model_settings.config: FreeCAD, other CAD tools). It can be
// read; its meshes belong in a new project (add_model).
var errNotSlicerProject = fmt.Errorf("%w: the file is a plain 3MF, not a slicer project; import its meshes into a project instead", ErrInvalid)

// ErrNotSlicerProject reports a mutation of a plain 3MF.
var ErrNotSlicerProject = errNotSlicerProject

// ensureSplit makes the project editable. A project in the split layout of
// Creality Print 7.2 is ready as it is. A slicer project that keeps its meshes
// inside 3D/3dmodel.model (Bambu Studio 2.x, older versions) is converted on
// the first mutation: every mesh object moves verbatim (triangle attributes,
// paint_* data included) into its own 3D/Objects/object_N.model, and the
// objects that used it get a component with p:path, exactly as the app writes
// them. A project that was not opened for a change is never converted:
// opening and saving it untouched still copies every member as it is.
func (p *Project) ensureSplit() error {
	if p.isNew || p.layout == layoutSplit {
		return nil
	}
	if !p.IsSlicerProject {
		return ErrNotSlicerProject
	}
	main, err := p.scanMember(memberModel)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsupportedLayout, err)
	}
	root := productionRoot(main.Root)

	// One sub model file per inline mesh object, in file order.
	backup := p.nextBackupID()
	paths := map[int]string{}
	fileNo := map[int]int{}
	for _, fo := range main.Objects {
		if fo.Mesh == nil {
			continue
		}
		if materialRefRE.Match(fo.Raw) {
			return fmt.Errorf("%w: mesh object %d uses 3MF materials (pid); save the project once in Creality Print, which stores its own colour data", ErrUnsupportedLayout, fo.ID)
		}
		if len(fo.Raw) == 0 {
			return fmt.Errorf("%w: mesh object %d cannot be moved", ErrUnsupportedLayout, fo.ID)
		}
		name := "3D/Objects/object_" + strconv.Itoa(backup) + ".model"
		p.setMember(name, rawObjectModel(root, fo.Raw))
		paths[fo.ID] = "/" + name
		fileNo[fo.ID] = backup
		backup++
	}

	maxID := p.maxID()
	moved := map[int]int{} // mesh object id that was an object itself -> its new wrapper id
	for _, o := range p.Objects {
		for _, part := range o.Parts {
			if part.Mesh.Path != "/"+memberModel {
				continue
			}
			target, ok := paths[part.Mesh.ObjectID]
			if !ok {
				return fmt.Errorf("%w: mesh object %d is not in %s", ErrUnsupportedLayout, part.Mesh.ObjectID, memberModel)
			}
			part.Mesh.Path = target
			no := fileNo[part.Mesh.ObjectID]
			if o.backupID == 0 {
				o.backupID = no
			}
			if !part.hasComponent {
				// The mesh object was itself the object of the build: it becomes the
				// mesh of a new wrapper object, which takes over its references.
				maxID++
				moved[o.ID] = maxID
				o.ID = maxID
				part.hasComponent = true
				part.ComponentTransform = mesh.Identity()
				part.inSettings = part.inSettings || o.inSettings
			}
		}
		if o.UUID == "" {
			suffix := objectUUIDSuffix
			if len(o.Parts) > 0 && o.Parts[0].Mesh.Shared {
				suffix = objectUUIDSuffix2
			}
			o.UUID = hex8(o.backupID) + suffix
		}
	}
	if len(moved) > 0 {
		for _, it := range p.Items {
			if n, ok := moved[it.ObjectID]; ok {
				it.ObjectID = n
				it.UUID = ""
			}
		}
		for _, pl := range p.Plates {
			for i := range pl.Instances {
				if n, ok := moved[pl.Instances[i].ObjectID]; ok {
					pl.Instances[i].ObjectID = n
				}
			}
		}
		for i := range p.Assemble {
			if n, ok := moved[p.Assemble[i].ObjectID]; ok {
				p.Assemble[i].ObjectID = n
				p.Assemble[i].Attrs.Set("object_id", strconv.Itoa(n))
			}
		}
		for i, id := range p.settingsOrder {
			if n, ok := moved[id]; ok {
				p.settingsOrder[i] = n
			}
		}
	}
	p.rootTag = root
	p.buildUUID = ""
	p.layout = layoutSplit
	p.modelDirty, p.settingsDirty = true, true
	return nil
}

// productionRoot makes sure a <model> start tag declares the production
// extension the split layout needs (older files do not).
func productionRoot(root string) string {
	if root == "" {
		return defaultRootTag
	}
	root = strings.TrimSuffix(strings.TrimSpace(root), ">")
	if !strings.Contains(root, "xmlns:p=") {
		root += ` xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06"`
	}
	if !strings.Contains(root, "requiredextensions=") {
		root += ` requiredextensions="p"`
	}
	return root + ">"
}

// rawObjectModel renders a sub model file around an <object> element that is
// moved as it was written.
func rawObjectModel(root string, raw []byte) []byte {
	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(root + "\n")
	b.WriteString(" <metadata name=\"BambuStudio:3mfVersion\">1</metadata>\n")
	b.WriteString(" <resources>\n  ")
	b.Write(raw)
	b.WriteString("\n </resources>\n <build/>\n</model>\n")
	return []byte(b.String())
}

// materialRefRE finds a reference to a 3MF material resource (pid, pindex or a
// triangle property index). The resources stay in 3D/3dmodel.model when a mesh
// moves to its own file, so such an object cannot be moved.
var materialRefRE = regexp.MustCompile(`\s(?:pid|pindex|p1|p2|p3)="`)
