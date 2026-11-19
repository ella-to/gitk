package ui

import (
	"ella.to/microui"
	"ella.to/microui/renderer"
)

// theme is the palette every pane draws with.
type theme struct {
	BG, Panel, Header, Border     microui.Color
	Sel, SelDim, Hover, Multi     microui.Color
	Text, Dim, Faint, Accent      microui.Color
	Green, Red, Yellow, Purple    microui.Color
	Cyan, Orange                  microui.Color
	AddBG, DelBG, AddHi, DelHi    microui.Color
	HunkBG, FileBG, Match         microui.Color
	Lanes                         []microui.Color
	BadgeLocal, BadgeHead         microui.Color
	BadgeRemote, BadgeTag, OnDark microui.Color
}

func darkTheme() theme {
	return theme{
		BG:     microui.Hex(0x1a1b26),
		Panel:  microui.Hex(0x1f2335),
		Header: microui.Hex(0x16161e),
		Border: microui.Hex(0x2f334d),
		Sel:    microui.Hex(0x2e3c64),
		SelDim: microui.Hex(0x262c44),
		Hover:  microui.Hex(0x232740),
		Multi:  microui.Hex(0x3b2f55),
		Text:   microui.Hex(0xc0caf5),
		Dim:    microui.Hex(0x7a83ad),
		Faint:  microui.Hex(0x545c7e),
		Accent: microui.Hex(0x7aa2f7),
		Green:  microui.Hex(0x9ece6a),
		Red:    microui.Hex(0xf7768e),
		Yellow: microui.Hex(0xe0af68),
		Purple: microui.Hex(0xbb9af7),
		Cyan:   microui.Hex(0x7dcfff),
		Orange: microui.Hex(0xff9e64),
		AddBG:  microui.Hex(0x1f2f24),
		DelBG:  microui.Hex(0x37222c),
		AddHi:  microui.Hex(0x2f5236),
		DelHi:  microui.Hex(0x5c2b3a),
		HunkBG: microui.Hex(0x22263a),
		FileBG: microui.Hex(0x2a2f4a),
		Match:  microui.Hex(0x4a4224),
		Lanes: []microui.Color{
			microui.Hex(0x7aa2f7), microui.Hex(0xbb9af7), microui.Hex(0x9ece6a), microui.Hex(0xff9e64),
			microui.Hex(0x7dcfff), microui.Hex(0xf7768e), microui.Hex(0xe0af68), microui.Hex(0x73daca),
		},
		BadgeLocal:  microui.Hex(0x9ece6a),
		BadgeHead:   microui.Hex(0x7aa2f7),
		BadgeRemote: microui.Hex(0xbb9af7),
		BadgeTag:    microui.Hex(0xe0af68),
		OnDark:      microui.Hex(0x16161e),
	}
}

func lightTheme() theme {
	t := darkTheme()
	t.BG = microui.Hex(0xf5f5f7)
	t.Panel = microui.Hex(0xebecf0)
	t.Header = microui.Hex(0xdcdee6)
	t.Border = microui.Hex(0xc8cbd6)
	t.Sel = microui.Hex(0xc9d7f8)
	t.SelDim = microui.Hex(0xdde4f5)
	t.Hover = microui.Hex(0xe6e9f2)
	t.Multi = microui.Hex(0xe3d5f7)
	t.Text = microui.Hex(0x2b2f3f)
	t.Dim = microui.Hex(0x5f6680)
	t.Faint = microui.Hex(0x9aa0b4)
	t.Accent = microui.Hex(0x2f5fd0)
	t.Green = microui.Hex(0x3d7a1f)
	t.Red = microui.Hex(0xc4304a)
	t.Yellow = microui.Hex(0x9a6b00)
	t.Purple = microui.Hex(0x7a3fc4)
	t.Cyan = microui.Hex(0x0f7a9c)
	t.Orange = microui.Hex(0xb35a12)
	t.AddBG = microui.Hex(0xe2f3dc)
	t.DelBG = microui.Hex(0xfae1e4)
	t.AddHi = microui.Hex(0xbfe5b0)
	t.DelHi = microui.Hex(0xf3b8c0)
	t.HunkBG = microui.Hex(0xe4e7f0)
	t.FileBG = microui.Hex(0xd3d8e8)
	t.Match = microui.Hex(0xf6e7b0)
	t.Lanes = []microui.Color{
		microui.Hex(0x2f5fd0), microui.Hex(0x7a3fc4), microui.Hex(0x3d7a1f), microui.Hex(0xb35a12),
		microui.Hex(0x0f7a9c), microui.Hex(0xc4304a), microui.Hex(0x9a6b00), microui.Hex(0x167a6a),
	}
	t.BadgeLocal = microui.Hex(0x3d7a1f)
	t.BadgeHead = microui.Hex(0x2f5fd0)
	t.BadgeRemote = microui.Hex(0x7a3fc4)
	t.BadgeTag = microui.Hex(0x9a6b00)
	t.OnDark = microui.Hex(0xffffff)
	return t
}

// applyStyle maps the palette onto microui's controls (dialogs, textboxes).
func (t *theme) applyStyle(st *microui.Style) {
	*st = renderer.TerminalStyle()
	st.Colors[microui.ColorText] = t.Text
	st.Colors[microui.ColorBorder] = microui.Color{}
	st.Colors[microui.ColorWindowBG] = t.Panel
	st.Colors[microui.ColorTitleBG] = t.Header
	st.Colors[microui.ColorTitleText] = t.Text
	st.Colors[microui.ColorPanelBG] = microui.Color{}
	st.Colors[microui.ColorButton] = t.Border
	st.Colors[microui.ColorButtonHover] = t.Sel
	st.Colors[microui.ColorButtonFocus] = t.Accent
	st.Colors[microui.ColorBase] = t.BG
	st.Colors[microui.ColorBaseHover] = t.Hover
	st.Colors[microui.ColorBaseFocus] = t.Header
	st.Colors[microui.ColorScrollBase] = t.Panel
	st.Colors[microui.ColorScrollThumb] = t.Border
	st.Colors[microui.ColorSelection] = t.Sel
}
