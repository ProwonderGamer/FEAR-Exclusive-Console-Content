package dbmerge

import "fmt"

// Inputs are the original files of the whole database pipeline.
type Inputs struct {
	PC, PCL     []byte   // stock PC FEAR.gamdb00p and FEARL.Gamdb00p (v1.08, from FEAR_8 / FEARL_8)
	PS3DB       []byte   // PS3 loki.gamedb
	PS3World    []byte   // PS3 sfod_d.wldsrvr
	PCWorlds    [][]byte // stock PC worlds Admin, Aftermath, Moody, ATC_Roof, Mapes_Elevator
	X360DB      []byte   // Xbox 360 Loki.gamedb (nil = SFOD-D only)
	TOC         TOC
	Strings     StringKeys // the SFOD-D string database (consistency check)
	SFODPS3Art  bool
	NoDropFix   bool // leave the SM15 drop fix out of the SFOD-D database
	BishopStock bool
	// Stage 4 (the complete mod's changes on top of stage 3; needs X360DB). NoCampaignPlayer leaves the stock campaign
	// player record alone; InstantAction adds the IA records (only together with the IA maps); WatsonReload adds
	// the Watson reload split. The complete mod = InstantAction + WatsonReload.
	NoCampaignPlayer bool
	InstantAction    bool
	WatsonReload     bool
	Log              *Log
}

// Outputs of the pipeline.
type Outputs struct {
	// Stage1 is the intermediate merge; SFOD is the SFOD-D database pair; Holiday the combined pair
	// (nil when no Xbox 360 database was given).
	Stage1  *Stage1Result
	SFOD    *Stage2Result
	Holiday *Stage3Result
	// Live is stage 4 on top of Holiday (nil when Holiday is nil): the databases the mod installs.
	Live *LiveResult
}

// BuildAll runs stage 1 (merge), stage 2 (sort-last names, mission order, animations, sound radii) and, when
// an Xbox 360 database is given, stage 3 (Holiday) and stage 4 (campaign player, Instant Action, Watson reload).
func BuildAll(in Inputs) (*Outputs, error) {
	log := in.Log
	if log == nil {
		log = NewLog()
	}
	s1, err := BuildStage1(Stage1Inputs{PC: in.PC, PCL: in.PCL, PS3DB: in.PS3DB, PS3World: in.PS3World, PCWorlds: in.PCWorlds, TOC: in.TOC, Log: log})
	if err != nil {
		return nil, fmt.Errorf("stage 1 (merge): %w", err)
	}
	s2, err := BuildStage2(Stage2Inputs{Stage1: s1, PC: in.PC, PCL: in.PCL, Strings: in.Strings, Log: log})
	if err != nil {
		return nil, fmt.Errorf("stage 2 (SFOD-D): %w", err)
	}
	out := &Outputs{Stage1: s1, SFOD: s2}
	if !in.NoDropFix {
		// the shipped SFOD-D database (update 3) additionally lists the SM15 weapons in PlayerWeapons; stage 3
		// starts from the unfixed database because it applies the same fix itself.
		fixed, err := BuildStage2(Stage2Inputs{Stage1: s1, PC: in.PC, PCL: in.PCL, Strings: in.Strings, DropFix: true, Log: NewLog()})
		if err != nil {
			return nil, fmt.Errorf("stage 2 (SFOD-D, drop fix): %w", err)
		}
		out.SFOD = fixed
	}
	if in.X360DB != nil {
		s3, err := BuildStage3(Stage3Inputs{PC: in.PC, PCL: in.PCL, SFODFEAR: s2.FEAR, SFODFEARL: s2.FEARL, X360DB: in.X360DB,
			PS3DB: in.PS3DB, TOC: in.TOC, SFODPS3Art: in.SFODPS3Art, BishopStock: in.BishopStock, Log: log})
		if err != nil {
			return nil, fmt.Errorf("stage 3 (Holiday): %w", err)
		}
		out.Holiday = s3
		live, err := BuildLive(LiveInputs{PC: in.PC, PCL: in.PCL, HolidayFEAR: s3.FEAR, HolidayFEARL: s3.FEARL,
			NoCampaignPlayer: in.NoCampaignPlayer, InstantAction: in.InstantAction, WatsonReload: in.WatsonReload, Log: log})
		if err != nil {
			return nil, fmt.Errorf("stage 4 (live): %w", err)
		}
		out.Live = live
	}
	return out, nil
}
