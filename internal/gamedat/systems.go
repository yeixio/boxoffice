package gamedat

import (
	"path"
	"slices"
	"strings"
	"unicode"
)

// System is a kind of machine games are for.
type System struct {
	// Key names it in the API and the database.
	Key string `json:"key"`
	// Name is what people see, and Short a name that fits a badge.
	Name  string `json:"name"`
	Short string `json:"short"`
	// Maker is who made it.
	Maker string `json:"maker"`
	// Arcade is MAME: games are sets named by short name, checked against
	// MAME's own list. The others are consoles, handhelds and computers.
	Arcade bool `json:"arcade"`
	// Hash says its games are matched to its list by their files' CRCs:
	// cartridges and floppies, small enough to read whole. Other systems'
	// games (discs, downloads, PC games) are known by the serial or title
	// ID written inside them, or else by their names.
	Hash bool `json:"hash"`
	// Lists are where its games are listed, in libretro's database
	// ("no-intro/Nintendo - Nintendo DS", "redump/Sony - PlayStation"), or
	// "titledb" for the Switch's eShop list. None: games are known by the
	// names they have, or the names written in them.
	Lists []string `json:"-"`
	// Libretro is the system's name in libretro's database and thumbnails.
	Libretro string `json:"-"`
	// Exts are its game files' extensions that no other system's have.
	Exts []string `json:"-"`
	// Shared are extensions other systems' games have too (.iso, .bin,
	// .chd): taken as its games in a folder that's its, or when what's
	// in the file says it's for this system.
	Shared []string `json:"-"`
	// GameFolders says a folder can be one game, as dumped PlayStation 3
	// and Vita games, Wii U games and PC games are.
	GameFolders bool `json:"-"`
	// Folders are names its folder often has, besides its own names.
	Folders []string `json:"-"`
	Header  Header   `json:"-"`
}

// Lists named by the system they're for.
func noIntroList(name string) []string { return []string{"no-intro/" + name} }
func redumpList(name string) []string  { return []string{"redump/" + name} }
func tosecList(name string) []string   { return []string{"tosec/" + name} }

// Extensions disc images and the like share between systems.
var (
	cdExts   = []string{".cue", ".bin", ".img", ".iso", ".chd", ".ccd", ".mds", ".m3u"}
	dvdExts  = []string{".iso", ".chd", ".cso", ".zso"}
	nintendo = []string{".iso", ".gcm", ".rvz", ".wia", ".gcz", ".ciso"}
)

// Systems are the systems Box Office knows, MAME first.
var Systems = []System{
	{Key: "mame", Name: "Arcade (MAME)", Short: "MAME", Maker: "Arcade", Arcade: true, Libretro: "MAME",
		Folders: []string{"mame", "arcade", "arcades", "mamecurrent", "mameroms", "roms", "fbneo", "finalburn"}},

	// Nintendo.
	{Key: "nes", Name: "Nintendo Entertainment System", Short: "NES", Maker: "Nintendo", Hash: true, Libretro: "Nintendo - Nintendo Entertainment System",
		Lists: noIntroList("Nintendo - Nintendo Entertainment System"), Exts: []string{".nes", ".unf", ".unif"}, Folders: []string{"nes", "famicom", "fc", "nintendo"}, Header: INES},
	{Key: "fds", Name: "Famicom Disk System", Short: "FDS", Maker: "Nintendo", Hash: true, Libretro: "Nintendo - Family Computer Disk System",
		Lists: noIntroList("Nintendo - Family Computer Disk System"), Exts: []string{".fds"}, Folders: []string{"fds", "famicomdisksystem", "familycomputerdisksystem"}, Header: FDS},
	{Key: "snes", Name: "Super Nintendo", Short: "SNES", Maker: "Nintendo", Hash: true, Libretro: "Nintendo - Super Nintendo Entertainment System",
		Lists: noIntroList("Nintendo - Super Nintendo Entertainment System"), Exts: []string{".sfc", ".smc", ".swc", ".fig"}, Folders: []string{"snes", "sfc", "superfamicom", "supernes", "supernintendo"}, Header: Copier},
	{Key: "satellaview", Name: "Satellaview", Short: "BS-X", Maker: "Nintendo", Hash: true, Libretro: "Nintendo - Satellaview",
		Lists: noIntroList("Nintendo - Satellaview"), Exts: []string{".bs"}, Folders: []string{"satellaview", "bsx"}},
	{Key: "sufami", Name: "Sufami Turbo", Short: "Sufami", Maker: "Bandai", Hash: true, Libretro: "Nintendo - Sufami Turbo",
		Lists: noIntroList("Nintendo - Sufami Turbo"), Shared: []string{".st"}, Folders: []string{"sufami", "sufamiturbo"}},
	{Key: "n64", Name: "Nintendo 64", Short: "N64", Maker: "Nintendo", Hash: true, Libretro: "Nintendo - Nintendo 64",
		Lists: noIntroList("Nintendo - Nintendo 64"), Exts: []string{".z64", ".n64", ".v64"}, Folders: []string{"n64", "nintendo64"}, Header: N64Order},
	{Key: "n64dd", Name: "Nintendo 64DD", Short: "64DD", Maker: "Nintendo", Hash: true, Libretro: "Nintendo - Nintendo 64DD",
		Lists: noIntroList("Nintendo - Nintendo 64DD"), Exts: []string{".ndd"}, Folders: []string{"n64dd", "64dd"}},
	{Key: "gamecube", Name: "GameCube", Short: "GC", Maker: "Nintendo", Libretro: "Nintendo - GameCube",
		Lists: redumpList("Nintendo - GameCube"), Exts: []string{".gcm"}, Shared: nintendo, Folders: []string{"gamecube", "gc", "ngc", "gcn", "dolphin"}},
	{Key: "wii", Name: "Wii", Short: "Wii", Maker: "Nintendo", Libretro: "Nintendo - Wii",
		Lists: append(redumpList("Nintendo - Wii"), "no-intro/Nintendo - Wii (Digital)"), Exts: []string{".wbfs", ".wad"}, Shared: nintendo, Folders: []string{"wii", "wiiware"}},
	{Key: "wiiu", Name: "Wii U", Short: "Wii U", Maker: "Nintendo", Libretro: "Nintendo - Wii U",
		Exts: []string{".wua", ".wux", ".wud", ".rpx"}, GameFolders: true, Folders: []string{"wiiu", "cemu"}},
	{Key: "switch", Name: "Nintendo Switch", Short: "Switch", Maker: "Nintendo", Libretro: "Nintendo - Switch",
		Lists: []string{"titledb"}, Exts: []string{".nsp", ".nsz", ".xci", ".xcz"}, Folders: []string{"switch", "nsw", "nintendoswitch", "yuzu", "ryujinx"}},
	{Key: "gb", Name: "Game Boy", Short: "GB", Maker: "Nintendo", Hash: true, Libretro: "Nintendo - Game Boy",
		Lists: noIntroList("Nintendo - Game Boy"), Exts: []string{".gb"}, Folders: []string{"gb", "gameboy"}},
	{Key: "gbc", Name: "Game Boy Color", Short: "GBC", Maker: "Nintendo", Hash: true, Libretro: "Nintendo - Game Boy Color",
		Lists: noIntroList("Nintendo - Game Boy Color"), Exts: []string{".gbc"}, Folders: []string{"gbc", "gameboycolor"}},
	{Key: "gba", Name: "Game Boy Advance", Short: "GBA", Maker: "Nintendo", Hash: true, Libretro: "Nintendo - Game Boy Advance",
		Lists: noIntroList("Nintendo - Game Boy Advance"), Exts: []string{".gba"}, Folders: []string{"gba", "gameboyadvance"}},
	{Key: "vb", Name: "Virtual Boy", Short: "VB", Maker: "Nintendo", Hash: true, Libretro: "Nintendo - Virtual Boy",
		Lists: noIntroList("Nintendo - Virtual Boy"), Exts: []string{".vb", ".vboy"}, Folders: []string{"vb", "virtualboy"}},
	{Key: "pokemini", Name: "Pokémon Mini", Short: "Mini", Maker: "Nintendo", Hash: true, Libretro: "Nintendo - Pokemon Mini",
		Lists: noIntroList("Nintendo - Pokemon Mini"), Exts: []string{".min"}, Folders: []string{"pokemini", "pokemonmini"}},
	{Key: "nds", Name: "Nintendo DS", Short: "DS", Maker: "Nintendo", Hash: true, Libretro: "Nintendo - Nintendo DS",
		Lists: noIntroList("Nintendo - Nintendo DS"), Exts: []string{".nds"}, Folders: []string{"nds", "ds", "nintendods"}},
	{Key: "dsi", Name: "Nintendo DSi", Short: "DSi", Maker: "Nintendo", Hash: true, Libretro: "Nintendo - Nintendo DSi",
		Lists: noIntroList("Nintendo - Nintendo DSi"), Exts: []string{".dsi"}, Folders: []string{"dsi", "nintendodsi"}},
	{Key: "3ds", Name: "Nintendo 3DS", Short: "3DS", Maker: "Nintendo", Libretro: "Nintendo - Nintendo 3DS",
		Lists: []string{"no-intro/Nintendo - Nintendo 3DS", "no-intro/Nintendo - New Nintendo 3DS", "no-intro/Nintendo - Nintendo 3DS (Digital)"},
		Exts:  []string{".3ds", ".cci", ".cia", ".cxi", ".3dsx"}, Folders: []string{"3ds", "n3ds", "nintendo3ds", "citra"}},

	// Sega.
	{Key: "sg1000", Name: "SG-1000", Short: "SG", Maker: "Sega", Hash: true, Libretro: "Sega - SG-1000",
		Lists: noIntroList("Sega - SG-1000"), Exts: []string{".sg"}, Folders: []string{"sg1000", "sg", "segasg1000"}},
	{Key: "sms", Name: "Sega Master System", Short: "SMS", Maker: "Sega", Hash: true, Libretro: "Sega - Master System - Mark III",
		Lists: noIntroList("Sega - Master System - Mark III"), Exts: []string{".sms"}, Folders: []string{"sms", "mastersystem", "segamastersystem", "markiii"}},
	{Key: "gamegear", Name: "Game Gear", Short: "GG", Maker: "Sega", Hash: true, Libretro: "Sega - Game Gear",
		Lists: noIntroList("Sega - Game Gear"), Exts: []string{".gg"}, Folders: []string{"gamegear", "gg", "segagamegear"}},
	{Key: "genesis", Name: "Sega Genesis / Mega Drive", Short: "Genesis", Maker: "Sega", Hash: true, Libretro: "Sega - Mega Drive - Genesis",
		Lists: noIntroList("Sega - Mega Drive - Genesis"), Exts: []string{".md", ".gen", ".smd"}, Shared: []string{".bin"}, Folders: []string{"genesis", "megadrive", "md", "segagenesis", "segamegadrive"}},
	{Key: "32x", Name: "Sega 32X", Short: "32X", Maker: "Sega", Hash: true, Libretro: "Sega - 32X",
		Lists: noIntroList("Sega - 32X"), Exts: []string{".32x"}, Folders: []string{"32x", "sega32x"}},
	{Key: "segacd", Name: "Sega CD / Mega-CD", Short: "Sega CD", Maker: "Sega", Libretro: "Sega - Mega-CD - Sega CD",
		Lists: redumpList("Sega - Mega-CD - Sega CD"), Shared: cdExts, Folders: []string{"segacd", "megacd", "scd"}},
	{Key: "saturn", Name: "Sega Saturn", Short: "Saturn", Maker: "Sega", Libretro: "Sega - Saturn",
		Lists: redumpList("Sega - Saturn"), Shared: cdExts, Folders: []string{"saturn", "segasaturn", "ss"}},
	{Key: "dreamcast", Name: "Dreamcast", Short: "DC", Maker: "Sega", Libretro: "Sega - Dreamcast",
		Lists: redumpList("Sega - Dreamcast"), Exts: []string{".gdi", ".cdi"}, Shared: cdExts, Folders: []string{"dreamcast", "dc", "segadreamcast"}},

	// Sony.
	{Key: "psx", Name: "PlayStation", Short: "PS1", Maker: "Sony", Libretro: "Sony - PlayStation",
		Lists: redumpList("Sony - PlayStation"), Shared: append([]string{".pbp", ".ecm"}, cdExts...), Folders: []string{"psx", "ps1", "playstation", "playstation1", "sonyplaystation", "psone"}},
	{Key: "ps2", Name: "PlayStation 2", Short: "PS2", Maker: "Sony", Libretro: "Sony - PlayStation 2",
		Lists: redumpList("Sony - PlayStation 2"), Shared: append([]string{".gz"}, dvdExts...), Folders: []string{"ps2", "playstation2", "pcsx2"}},
	{Key: "ps3", Name: "PlayStation 3", Short: "PS3", Maker: "Sony", Libretro: "Sony - PlayStation 3",
		Lists:  []string{"redump/Sony - PlayStation 3", "no-intro/Sony - PlayStation 3 (PSN)"},
		Shared: []string{".iso", ".pkg"}, GameFolders: true, Folders: []string{"ps3", "playstation3", "rpcs3"}},
	{Key: "ps4", Name: "PlayStation 4", Short: "PS4", Maker: "Sony", Libretro: "Sony - PlayStation 4",
		Shared: []string{".pkg"}, GameFolders: true, Folders: []string{"ps4", "playstation4"}},
	{Key: "ps5", Name: "PlayStation 5", Short: "PS5", Maker: "Sony", Libretro: "Sony - PlayStation 5",
		GameFolders: true, Folders: []string{"ps5", "playstation5"}},
	{Key: "psp", Name: "PlayStation Portable", Short: "PSP", Maker: "Sony", Libretro: "Sony - PlayStation Portable",
		Lists:  []string{"redump/Sony - PlayStation Portable", "no-intro/Sony - PlayStation Portable (PSN)"},
		Shared: []string{".iso", ".cso", ".zso", ".pbp", ".chd"}, GameFolders: true, Folders: []string{"psp", "playstationportable", "ppsspp"}},
	{Key: "vita", Name: "PlayStation Vita", Short: "Vita", Maker: "Sony", Libretro: "Sony - PlayStation Vita",
		Lists: []string{"no-intro/Sony - PlayStation Vita", "no-intro/Sony - PlayStation Vita (PSN)"},
		Exts:  []string{".vpk"}, GameFolders: true, Folders: []string{"vita", "psvita", "psv", "playstationvita", "vita3k"}},

	// Microsoft.
	{Key: "xbox", Name: "Xbox", Short: "Xbox", Maker: "Microsoft", Libretro: "Microsoft - Xbox",
		Lists: redumpList("Microsoft - Xbox"), Exts: []string{".xiso"}, Shared: []string{".iso"}, GameFolders: true, Folders: []string{"xbox", "originalxbox", "xemu"}},
	{Key: "xbox360", Name: "Xbox 360", Short: "360", Maker: "Microsoft", Libretro: "Microsoft - Xbox 360",
		Lists: redumpList("Microsoft - Xbox 360"), Shared: []string{".iso", ".xex"}, GameFolders: true, Folders: []string{"xbox360", "x360", "360", "xenia"}},
	{Key: "xboxone", Name: "Xbox One / Series", Short: "XB1", Maker: "Microsoft", Libretro: "Microsoft - Xbox One",
		GameFolders: true, Folders: []string{"xboxone", "xb1", "xboxseries", "xboxseriesx"}},

	// NEC and SNK.
	{Key: "pce", Name: "PC Engine / TurboGrafx-16", Short: "PCE", Maker: "NEC", Hash: true, Libretro: "NEC - PC Engine - TurboGrafx 16",
		Lists: noIntroList("NEC - PC Engine - TurboGrafx 16"), Exts: []string{".pce"}, Folders: []string{"pce", "pcengine", "tg16", "turbografx", "turbografx16"}},
	{Key: "supergrafx", Name: "PC Engine SuperGrafx", Short: "SGX", Maker: "NEC", Hash: true, Libretro: "NEC - PC Engine SuperGrafx",
		Lists: noIntroList("NEC - PC Engine SuperGrafx"), Exts: []string{".sgx"}, Folders: []string{"supergrafx", "sgx"}},
	{Key: "pcecd", Name: "PC Engine CD / TurboGrafx-CD", Short: "PCE-CD", Maker: "NEC", Libretro: "NEC - PC Engine CD - TurboGrafx-CD",
		Lists: redumpList("NEC - PC Engine CD - TurboGrafx-CD"), Shared: cdExts, Folders: []string{"pcecd", "pcenginecd", "tgcd", "turbografxcd", "turbografx16cd"}},
	{Key: "pcfx", Name: "PC-FX", Short: "PC-FX", Maker: "NEC", Libretro: "NEC - PC-FX",
		Lists: redumpList("NEC - PC-FX"), Shared: cdExts, Folders: []string{"pcfx"}},
	{Key: "pc98", Name: "PC-98", Short: "PC-98", Maker: "NEC", Libretro: "NEC - PC-98",
		Lists: redumpList("NEC - PC-98"), Exts: []string{".hdi", ".fdi", ".d88", ".nhd"}, Shared: cdExts, Folders: []string{"pc98", "pc9801"}},
	{Key: "ngp", Name: "Neo Geo Pocket", Short: "NGP", Maker: "SNK", Hash: true, Libretro: "SNK - Neo Geo Pocket",
		Lists: noIntroList("SNK - Neo Geo Pocket"), Exts: []string{".ngp"}, Folders: []string{"ngp", "neogeopocket"}},
	{Key: "ngpc", Name: "Neo Geo Pocket Color", Short: "NGPC", Maker: "SNK", Hash: true, Libretro: "SNK - Neo Geo Pocket Color",
		Lists: noIntroList("SNK - Neo Geo Pocket Color"), Exts: []string{".ngc"}, Folders: []string{"ngpc", "neogeopocketcolor"}},
	{Key: "neocd", Name: "Neo Geo CD", Short: "NGCD", Maker: "SNK", Libretro: "SNK - Neo Geo CD",
		Lists: redumpList("SNK - Neo Geo CD"), Shared: cdExts, Folders: []string{"neocd", "neogeocd", "ngcd"}},

	// Atari.
	{Key: "atari2600", Name: "Atari 2600", Short: "2600", Maker: "Atari", Hash: true, Libretro: "Atari - 2600",
		Lists: noIntroList("Atari - 2600"), Exts: []string{".a26"}, Shared: []string{".bin"}, Folders: []string{"atari2600", "a2600", "2600", "vcs"}},
	{Key: "atari5200", Name: "Atari 5200", Short: "5200", Maker: "Atari", Hash: true, Libretro: "Atari - 5200",
		Lists: noIntroList("Atari - 5200"), Exts: []string{".a52"}, Shared: []string{".bin"}, Folders: []string{"atari5200", "a5200", "5200"}},
	{Key: "atari7800", Name: "Atari 7800", Short: "7800", Maker: "Atari", Hash: true, Libretro: "Atari - 7800",
		Lists: noIntroList("Atari - 7800"), Exts: []string{".a78"}, Shared: []string{".bin"}, Folders: []string{"atari7800", "a7800", "7800"}, Header: A7800},
	{Key: "atari800", Name: "Atari 8-bit computers", Short: "A800", Maker: "Atari", Hash: true, Libretro: "Atari - 8-bit Family",
		Lists: noIntroList("Atari - 8-bit Family"), Exts: []string{".atr", ".atx", ".car", ".xfd"}, Shared: []string{".bin", ".rom", ".xex"}, Folders: []string{"atari800", "atari8bit", "a800", "atarixl"}},
	{Key: "lynx", Name: "Atari Lynx", Short: "Lynx", Maker: "Atari", Hash: true, Libretro: "Atari - Lynx",
		Lists: noIntroList("Atari - Lynx"), Exts: []string{".lnx"}, Folders: []string{"lynx", "atarilynx"}, Header: Lynx},
	{Key: "jaguar", Name: "Atari Jaguar", Short: "Jaguar", Maker: "Atari", Hash: true, Libretro: "Atari - Jaguar",
		Lists: noIntroList("Atari - Jaguar"), Exts: []string{".j64", ".jag"}, Folders: []string{"jaguar", "atarijaguar"}},
	{Key: "jaguarcd", Name: "Atari Jaguar CD", Short: "JagCD", Maker: "Atari", Libretro: "Atari - Jaguar CD",
		Lists: redumpList("Atari - Jaguar CD"), Shared: cdExts, Folders: []string{"jaguarcd", "atarijaguarcd"}},
	{Key: "atarist", Name: "Atari ST", Short: "ST", Maker: "Atari", Hash: true, Libretro: "Atari - ST",
		Lists: noIntroList("Atari - ST"), Exts: []string{".stx", ".msa"}, Shared: []string{".st"}, Folders: []string{"atarist", "st", "ataristfalcon"}},

	// Others.
	{Key: "wonderswan", Name: "WonderSwan", Short: "WS", Maker: "Bandai", Hash: true, Libretro: "Bandai - WonderSwan",
		Lists: noIntroList("Bandai - WonderSwan"), Exts: []string{".ws"}, Folders: []string{"ws", "wonderswan"}},
	{Key: "wonderswancolor", Name: "WonderSwan Color", Short: "WSC", Maker: "Bandai", Hash: true, Libretro: "Bandai - WonderSwan Color",
		Lists: noIntroList("Bandai - WonderSwan Color"), Exts: []string{".wsc"}, Folders: []string{"wsc", "wonderswancolor"}},
	{Key: "colecovision", Name: "ColecoVision", Short: "Coleco", Maker: "Coleco", Hash: true, Libretro: "Coleco - ColecoVision",
		Lists: noIntroList("Coleco - ColecoVision"), Exts: []string{".col"}, Shared: []string{".bin", ".rom"}, Folders: []string{"colecovision", "coleco"}},
	{Key: "intellivision", Name: "Intellivision", Short: "Intv", Maker: "Mattel", Hash: true, Libretro: "Mattel - Intellivision",
		Lists: noIntroList("Mattel - Intellivision"), Exts: []string{".int"}, Shared: []string{".bin", ".rom"}, Folders: []string{"intellivision", "intv"}},
	{Key: "vectrex", Name: "Vectrex", Short: "Vectrex", Maker: "GCE", Hash: true, Libretro: "GCE - Vectrex",
		Lists: noIntroList("GCE - Vectrex"), Exts: []string{".vec"}, Shared: []string{".bin", ".gam"}, Folders: []string{"vectrex"}},
	{Key: "odyssey2", Name: "Odyssey² / Videopac", Short: "O2", Maker: "Magnavox", Hash: true, Libretro: "Magnavox - Odyssey2",
		Lists: noIntroList("Magnavox - Odyssey2"), Shared: []string{".bin"}, Folders: []string{"odyssey2", "o2", "videopac", "odyssey"}},
	{Key: "channelf", Name: "Channel F", Short: "Ch F", Maker: "Fairchild", Hash: true, Libretro: "Fairchild - Channel F",
		Lists: noIntroList("Fairchild - Channel F"), Exts: []string{".chf"}, Shared: []string{".bin"}, Folders: []string{"channelf", "fairchild"}},
	{Key: "supervision", Name: "Supervision", Short: "SV", Maker: "Watara", Hash: true, Libretro: "Watara - Supervision",
		Lists: noIntroList("Watara - Supervision"), Exts: []string{".sv"}, Shared: []string{".bin"}, Folders: []string{"supervision", "watara"}},
	{Key: "gamecom", Name: "Game.com", Short: "Game.com", Maker: "Tiger", Hash: true, Libretro: "Tiger - Game.com",
		Lists: noIntroList("Tiger - Game.com"), Exts: []string{".tgc"}, Shared: []string{".bin"}, Folders: []string{"gamecom", "tigergamecom"}},
	{Key: "3do", Name: "3DO", Short: "3DO", Maker: "The 3DO Company", Libretro: "The 3DO Company - 3DO",
		Lists: redumpList("The 3DO Company - 3DO"), Shared: cdExts, Folders: []string{"3do", "panasonic3do"}},
	{Key: "cdi", Name: "CD-i", Short: "CD-i", Maker: "Philips", Libretro: "Philips - CD-i",
		Lists: redumpList("Philips - CD-i"), Shared: cdExts, Folders: []string{"cdi", "philipscdi"}},

	// Computers.
	{Key: "msx", Name: "MSX", Short: "MSX", Maker: "Microsoft", Hash: true, Libretro: "Microsoft - MSX",
		Lists: noIntroList("Microsoft - MSX"), Exts: []string{".mx1"}, Shared: []string{".rom", ".dsk", ".cas"}, Folders: []string{"msx", "msx1"}},
	{Key: "msx2", Name: "MSX2", Short: "MSX2", Maker: "Microsoft", Hash: true, Libretro: "Microsoft - MSX2",
		Lists: noIntroList("Microsoft - MSX2"), Exts: []string{".mx2"}, Shared: []string{".rom", ".dsk"}, Folders: []string{"msx2", "msx2plus"}},
	{Key: "c64", Name: "Commodore 64", Short: "C64", Maker: "Commodore", Hash: true, Libretro: "Commodore - 64",
		Lists: noIntroList("Commodore - 64"), Exts: []string{".crt", ".d64", ".t64", ".prg", ".g64"}, Shared: []string{".tap"}, Folders: []string{"c64", "commodore64"}},
	{Key: "amiga", Name: "Amiga", Short: "Amiga", Maker: "Commodore", Hash: true, Libretro: "Commodore - Amiga",
		Lists: noIntroList("Commodore - Amiga"), Exts: []string{".adf", ".ipf", ".hdf", ".lha"}, Folders: []string{"amiga", "commodoreamiga"}},
	{Key: "cd32", Name: "Amiga CD32", Short: "CD32", Maker: "Commodore", Libretro: "Commodore - CD32",
		Lists: redumpList("Commodore - CD32"), Shared: cdExts, Folders: []string{"cd32", "amigacd32"}},
	{Key: "cdtv", Name: "CDTV", Short: "CDTV", Maker: "Commodore", Libretro: "Commodore - CDTV",
		Lists: redumpList("Commodore - CDTV"), Shared: cdExts, Folders: []string{"cdtv", "amigacdtv"}},
	{Key: "cpc", Name: "Amstrad CPC", Short: "CPC", Maker: "Amstrad", Hash: true, Libretro: "Amstrad - CPC",
		Lists: tosecList("Amstrad - CPC"), Exts: []string{".cdt", ".sna"}, Shared: []string{".dsk"}, Folders: []string{"cpc", "amstradcpc", "amstrad"}},
	{Key: "zxspectrum", Name: "ZX Spectrum", Short: "ZX", Maker: "Sinclair", Hash: true, Libretro: "Sinclair - ZX Spectrum",
		Lists: tosecList("Sinclair - ZX Spectrum"), Exts: []string{".tzx", ".z80", ".szx"}, Shared: []string{".tap", ".sna", ".dsk"}, Folders: []string{"zxspectrum", "spectrum", "zx", "zxs"}},
	{Key: "apple2", Name: "Apple II", Short: "Apple II", Maker: "Apple", Hash: true, Libretro: "Apple - II",
		Lists: tosecList("Apple - II"), Exts: []string{".woz", ".nib", ".po", ".do"}, Shared: []string{".dsk"}, Folders: []string{"apple2", "appleii", "apple2e"}},
	{Key: "x68000", Name: "X68000", Short: "X68K", Maker: "Sharp", Hash: true, Libretro: "Sharp - X68000",
		Lists: noIntroList("Sharp - X68000"), Exts: []string{".dim", ".xdf"}, Folders: []string{"x68000", "x68k"}},
	{Key: "fmtowns", Name: "FM Towns", Short: "Towns", Maker: "Fujitsu", Libretro: "Fujitsu - FM Towns",
		Lists: redumpList("Fujitsu - FM Towns"), Shared: cdExts, Folders: []string{"fmtowns", "towns"}},
	{Key: "dos", Name: "MS-DOS", Short: "DOS", Maker: "PC", Libretro: "DOS",
		GameFolders: true, Shared: []string{".exe", ".com", ".bat", ".zip", ".iso", ".conf"}, Folders: []string{"dos", "msdos", "dosbox", "exodos", "pcdos"}},
	{Key: "pc", Name: "PC (Windows)", Short: "PC", Maker: "PC", Libretro: "Microsoft - Windows",
		GameFolders: true, Shared: []string{".exe", ".iso", ".msi"}, Folders: []string{"pc", "windows", "win", "pcgames", "windowsgames", "steam", "gog", "steamlibrary"}},
}

// LookupSystem finds a system by key.
func LookupSystem(key string) (System, bool) {
	i := slices.IndexFunc(Systems, func(s System) bool { return s.Key == key })
	if i < 0 {
		return System{}, false
	}
	return Systems[i], true
}

// fold makes a folder name comparable: lower case letters and digits only,
// so "Super Nintendo", "super-nintendo" and "SuperNintendo" are one.
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SystemByFolder finds the system a folder's name says it's for: its key,
// its names, libretro's name for it, or a usual folder name.
func SystemByFolder(name string) (System, bool) {
	f := fold(name)
	if f == "" {
		return System{}, false
	}
	for _, s := range Systems {
		if f == s.Key || f == fold(s.Name) || f == fold(s.Short) || f == fold(s.Libretro) || slices.Contains(s.Folders, f) {
			// "roms" alone says nothing but that it holds games.
			if f == "roms" {
				continue
			}
			return s, true
		}
	}
	return System{}, false
}

// SystemByExt finds the system a game file's extension is for, when only
// one system's games have it.
func SystemByExt(name string) (System, bool) {
	ext := strings.ToLower(path.Ext(name))
	for _, s := range Systems {
		if slices.Contains(s.Exts, ext) {
			return s, true
		}
	}
	return System{}, false
}

// Takes says whether a file's extension is one the system's games have.
func (s System) Takes(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext != "" && (slices.Contains(s.Exts, ext) || slices.Contains(s.Shared, ext))
}

// Disc says whether the system's games are discs, downloads or folders
// known by what's written in them rather than by hash.
func (s System) Disc() bool { return !s.Arcade && !s.Hash }

// SharedExt says whether a file's extension is one several systems'
// games have, such as .iso or .chd: what's in it says which.
func SharedExt(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	switch ext {
	case "", ".exe", ".com", ".bat", ".conf", ".msi", ".gz", ".m3u", ".ecm", ".ccd", ".mds", ".zip":
		// Too common to say a folder holds games.
		return false
	}
	for _, s := range Systems {
		if slices.Contains(s.Shared, ext) {
			return true
		}
	}
	return false
}

// IsGameFile says whether a file looks like a game: a known system's
// file, a disc image, or an archive.
func IsGameFile(name string) bool {
	if IsArchive(name) || SharedExt(name) {
		return true
	}
	_, ok := SystemByExt(name)
	return ok
}
