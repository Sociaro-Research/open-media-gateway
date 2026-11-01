package kling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

const (
	KlingEffects      = "kling-video-effects"
	ApparelReplicator = "kling-apparel-replicator"
	GoodsStudio       = "kling-goods-studio"
	VideoCommerce     = "kling-video-commerce"
	VirtualTryOn      = "kling-virtual-try-on"
)

var apparelVoices = []string{"energetic_sales_girl", "gentle_sales_woman", "crisp_sales_girl", "mature_sales_woman", "calm_sales_woman", "soft_sales_woman", "energetic_sales_boy", "enthusiastic_sales_man", "steady_sales_man", "calm_sales_man"}
var commerceVoices = []string{"male_calm_informative", "female_clear_rational", "female_gentle_soothing", "female_bright_engaging", "male_crisp_persuasive", "female_intelligent_narrative", "male_clear_professional", "male_energetic_sporty", "female_warm_rich"}
var commerceAvatars = []string{"avatar_eva_female", "avatar_oliver_male", "avatar_elena_female", "avatar_adam_male", "avatar_anna_female", "avatar_rajput_male", "avatar_mia_female", "avatar_river_male", "avatar_marcus_male", "avatar_vivian_female", "avatar_lee_male", "avatar_toto_cartoon"}
var dualImageEffects = []string{"pet_skateboard", "daily_ootd", "toss_run", "switch_to_silk", "studio_look", "french_elegance", "finger_swipe", "smooth_transition", "kiss_pro", "snow_night_kiss", "eternal_kiss", "cheers_2026", "fight_pro", "hug_pro", "heart_gesture_pro"}

// All scenes are exact native enums from the reviewed Effects request schema.
var effectScenes = []string{
	"net_the_fish",
	"birthday_board",
	"summer_fireworks",
	"blow_blaze",
	"champagne_birthday",
	"pet_working_daily",
	"pet_roll_window",
	"pet_crash_earth",
	"makeup_style",
	"hair_dressup",
	"barbie_transform",
	"critter_paste",
	"wiggle_faces_pets",
	"paw_headbang",
	"food_splash",
	"dj_pet",
	"pet_diving",
	"disco_paws",
	"pet_fitness",
	"j_var_live",
	"pet_burst",
	"idol_live",
	"beast_striker",
	"african_sway",
	"jealousy_sway",
	"chubby_fish",
	"shrug_dance",
	"soccer_star",
	"bra_hot_dance",
	"arg_hot_dance",
	"fra_hot_dance",
	"bra_goal",
	"arg_goal",
	"por_goal",
	"victory_slide",
	"samba_fever",
	"bicycle_kick",
	"football_dance_2",
	"red_card_sent_off",
	"football_dance",
	"magic_world_vlog",
	"magic_world_vlog_9_16",
	"air_dunk",
	"whirling_beverage_9_16",
	"tennis_trend",
	"tennis_trend_9_16",
	"football_live_9_16",
	"whirling_beverage",
	"f1_live",
	"football_live",
	"spielberg_transition",
	"korean_baseball_9_16",
	"korean_baseball",
	"pet_skateboard",
	"daily_ootd",
	"tiny_beast_printer",
	"landmark_reveal",
	"winter_charm",
	"flash_ride",
	"maestro_of_magic",
	"magic_carpet_ride",
	"good_luck_spirit",
	"shooting_star",
	"sparkler_wand",
	"sovereign_scepter",
	"dirt_rush",
	"return_of_the_king",
	"dance_with_dragon",
	"minimalist_light",
	"martial_meow",
	"sassy_shake",
	"knock_at_a_door_revenge",
	"palm_sized_figure_pro",
	"prank_box",
	"perler_beads",
	"spring_bloom",
	"toss_run",
	"switch_to_silk",
	"get_rich_quick",
	"make_it_rain",
	"twist_shake",
	"the_hip_sway",
	"send_my_love",
	"funky_martian",
	"wealth_drive",
	"the_high_kick",
	"the_exercise",
	"lucky_veggie",
	"studio_look",
	"flash_drive",
	"shush_my_dreams",
	"french_elegance",
	"finger_swipe",
	"advent_of_flora",
	"smooth_transition",
	"kiss_pro",
	"raid_check",
	"snow_night_kiss",
	"eternal_kiss",
	"fortune_in_motion",
	"chinese_trend",
	"sedan_chair_dance",
	"skyfall",
	"good_luck_dance",
	"laicai_dance",
	"yangge_dance",
	"color_mixing",
	"lantern_festival_cuju",
	"unique_firework",
	"unique_spring_couplets",
	"horse_mask",
	"fortune_knocks_cartoon",
	"tangyuan_to_animal",
	"hot_feet_dance",
	"swag_dance",
	"pigeon_dance",
	"bloodline_dance",
	"chanel_dance",
	"cute_dance",
	"love_theme_song",
	"pumpitup_dance",
	"city_to_village",
	"fortune_god_transform",
	"new_year_feast",
	"ring_in_new",
	"horse_year_firework",
	"crystal_horse",
	"drunk_dance",
	"drunk_dance_pet",
	"daoma_dance",
	"bouncy_dance",
	"smooth_sailing_dance",
	"new_year_greeting",
	"lion_dance",
	"prosperity",
	"great_success",
	"golden_horse_fortune",
	"red_packet_box",
	"lucky_horse_year",
	"lucky_red_packet",
	"lucky_money_come",
	"lion_dance_pet",
	"dumpling_making_pet",
	"fish_making_pet",
	"pet_red_packet",
	"lantern_glow",
	"expression_challenge",
	"overdrive",
	"heart_gesture_dance",
	"poping",
	"martial_arts",
	"running",
	"nezha",
	"motorcycle_dance",
	"subject_3_dance",
	"ghost_step_dance",
	"phantom_jewel",
	"zoom_out",
	"cheers_2026",
	"fight_pro",
	"hug_pro",
	"heart_gesture_pro",
	"dollar_rain_pro",
	"pet_bee_pro",
	"countdown_teleport",
	"santa_random_surprise",
	"magic_match_tree",
	"bullet_time_360",
	"happy_birthday",
	"birthday_star",
	"thumbs_up_pro",
	"tiger_hug_pro",
	"pet_lion_pro",
	"surprise_bouquet",
	"bouquet_drop",
	"glamour_photo_shoot",
	"box_of_joy",
	"first_toast_of_the_year",
	"my_santa_pic",
	"santa_gift",
	"steampunk_christmas",
	"snowglobe",
	"christmas_photo_shoot",
	"ornament_crash",
	"santa_express",
	"instant_christmas",
	"coronation_of_frost",
	"building_sweater",
	"spark_in_the_snow",
	"scarlet_and_snow",
	"bullet_time_lite",
	"jumping_ginger_joy",
	"pure_white_wings",
	"black_wings",
	"golden_wing",
	"pink_pink_wings",
	"venomous_spider",
	"throne_of_king",
	"luminous_elf",
	"woodland_elf",
	"guardian_spirit",
	"swish_swish",
	"snowboarding",
	"witch_transform",
	"vampire_transform",
	"pumpkin_head_transform",
	"demon_transform",
	"mummy_transform",
	"zombie_transform",
	"cute_pumpkin_transform",
	"halloween_escape",
	"pet_moto_rider",
	"running_man",
	"3d_cartoon_2",
	"pet_dance",
	"swing_swing",
	"day_to_night",
	"surfsurf",
	"skateskate",
}

func solutionPath(id string) string {
	switch id {
	case KlingEffects:
		return "/v1/videos/effects"
	case ApparelReplicator:
		return "/solutions/e-commerce/clothing_dupe"
	case GoodsStudio:
		return "/solutions/e-commerce/goods_studio"
	case VideoCommerce:
		return "/solutions/talking_agent"
	case VirtualTryOn:
		return "/solutions/virtual_try_on"
	}
	return ""
}
func isSolutionOperation(id string) bool { return solutionPath(id) != "" }
func solutionText(key, label string, limit int) core.ParameterField {
	return core.ParameterField{Key: key, Label: label, Type: "text", MaxLength: limit}
}
func solutionSelect(key, label string, choices []string, required bool) core.ParameterField {
	return core.ParameterField{Key: key, Label: label, Type: "select", Choices: slices.Clone(choices), Required: required}
}
func solutionModel(id string) core.MediaModel {
	m := core.MediaModel{ID: id, MediaType: "video", NoSeed: true, NoLanguage: true, NoInstructions: true, MaxOutputs: 1, SupportsImageInput: true, ReferenceMediaLimits: &core.ReferenceMediaLimits{Image: core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png", "image/webp"}, MaxBytes: 50 << 20, MinDimension: 300, MinRatio: .4, MaxRatio: 2.5}}, Pricing: core.MediaModelPricing{Known: false, ReferenceDescription: "Published per-output-second rates require actual output duration; no fixed output duration is accepted by this operation."}}
	m.Resolutions = []string{"720p", "1080p"}
	switch id {
	case KlingEffects:
		m.DisplayName = "Kling · Video Effects"
		m.NoPrompt = true
		m.NoWatermark = true
		m.Resolutions = nil
		m.MinImageInputs = 1
		m.MaxImageInputs = 2
		m.RequiresImageInput = true
		m.ReferenceModes = []core.ReferenceMode{{ID: "single_image", Label: "Single image", MinImages: 1, MaxImages: 1}, {ID: "dual_image", Label: "Left and right images", MinImages: 2, MaxImages: 2, ImageRoles: []string{"left", "right"}}}
		m.ReferenceRoleSchemas = []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"reference", "left", "right"}, Default: "reference"}}
		m.Parameters = []core.ParameterField{solutionSelect("effect_scene", "Effect scene", effectScenes, true)}
		m.ReferenceMediaLimits.Image.ContentTypes = []string{"image/jpeg", "image/png"}
		m.ReferenceMediaLimits.Image.MaxBytes = 10 << 20
		m.Pricing.ReferenceDescription = "Effects tariffs vary by template; the reviewed pricing source supplies no numeric template prices."
	case ApparelReplicator:
		m.DisplayName = "Kling · AI Apparel Replicator"
		m.MaxPromptCharacters = 2500
		m.SupportsVideoInput = true
		m.RequiresVideoInput = true
		m.SupportsAudioInput = true
		m.RequiresImageInput = true
		m.MinImageInputs = 1
		m.MaxImageInputs = 10
		m.SupportsVoice = true
		m.Voices = slices.Clone(apparelVoices)
		m.DurationFromInput = true
		m.Ratios = []string{"9:16", "2:3", "3:4", "1:1", "4:3", "3:2", "16:9", "21:9"}
		m.RatioMayBeInferred = true
		m.ReferenceModes = []core.ReferenceMode{{ID: "references", Label: "Product, model and source video", MinImages: 1, MaxImages: 10, MinVideos: 1, MaxVideos: 1, MaxAudios: 1}}
		m.ReferenceRoleSchemas = []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"product_image", "model_image"}, Default: "product_image"}, {Kind: "video", Roles: []string{"source_video"}, Default: "source_video"}, {Kind: "audio", Roles: []string{"bgm"}, Default: "bgm"}}
		m.ReferenceMediaLimits.Video = core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, MaxBytes: 100 << 20, MinDuration: 5, MaxDuration: 180, MinDimension: 576, MaxDimension: 4553, MaxPixels: 8294400, MinRatio: .4, MaxRatio: 2, MinFPS: 24, MaxFPS: 60}
		m.ReferenceMediaLimits.Audio = core.MediaFileLimits{ContentTypes: []string{"audio/mpeg", "audio/mp4", "audio/x-m4a", "audio/wav", "audio/wave", "audio/x-wav"}, MaxBytes: 50 << 20}
	case GoodsStudio:
		m.DisplayName = "Kling · Goods Studio"
		m.MaxPromptCharacters = 200
		m.RequiresImageInput = true
		m.MinImageInputs = 1
		m.MaxImageInputs = 5
		m.Ratios = []string{"9:16", "1:1", "16:9"}
		m.ReferenceModes = []core.ReferenceMode{{ID: "references", Label: "Product images", MinImages: 1, MaxImages: 5}}
		m.ReferenceRoleSchemas = []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"ref_image"}, Default: "ref_image"}}
		m.Parameters = []core.ParameterField{solutionSelect("duration", "Output duration", []string{"15", "30", "60"}, true), solutionText("goods_description", "Product description", 2000)}
		m.Pricing = core.MediaModelPricing{Known: true, ReferencePriceUSD: 3.57, ReferenceDescription: "15-second 720p output at published USD balance list price; actual output duration and account deductions remain separate."}
	case VideoCommerce:
		m.DisplayName = "Kling · Video Commerce"
		m.MaxPromptCharacters = 2000
		m.MaxImageInputs = 6
		m.SupportsVoice = true
		m.Voices = slices.Clone(commerceVoices)
		m.DurationMayBeInferred = true
		m.Ratios = []string{"9:16", "16:9"}
		m.ReferenceModes = []core.ReferenceMode{{ID: "references", Label: "Avatar and product images", MaxImages: 6}}
		m.ReferenceRoleSchemas = []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"avatar_image", "ref_image"}, Default: "avatar_image"}}
		m.Parameters = []core.ParameterField{solutionSelect("avatar_id", "Library avatar", commerceAvatars, false), solutionText("goods_title", "Product title", 500), solutionText("goods_price", "Product price", 256), {Key: "goods_target_audience", Label: "Target audiences", Type: "text_list", MaxItems: 20, MaxLength: 200}, {Key: "goods_selling_point", Label: "Selling points", Type: "text_list", MaxItems: 20, MaxLength: 500}, solutionSelect("speech_rate", "Speech rate", []string{"0.8", "1.0", "1.2"}, false), solutionText("action_prompt", "Avatar action prompt", 2500), {Key: "allow_polish", Label: "Allow script polishing", Type: "boolean"}, {Key: "bgm_enabled", Label: "Background music", Type: "boolean"}}
	case VirtualTryOn:
		m.DisplayName = "Kling · Virtual Try-On"
		m.MediaType = "image"
		m.NoPrompt = true
		m.Resolutions = nil
		m.RequiresImageInput = true
		m.MinImageInputs = 2
		m.MaxImageInputs = 2
		m.ReferenceModes = []core.ReferenceMode{{ID: "references", Label: "Product and person", MinImages: 2, MaxImages: 2}}
		m.ReferenceRoleSchemas = []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"product_image", "person_image"}, Default: "product_image"}}
		for _, key := range []string{"keep_face", "keep_pose", "keep_background"} {
			m.Parameters = append(m.Parameters, core.ParameterField{Key: key, Label: strings.ReplaceAll(key, "_", " "), Type: "boolean", Default: true})
		}
		m.ReferenceMediaLimits.Image.MaxBytes = 10 << 20
		m.ReferenceMediaLimits.Image.MinDimension = 301
		m.ReferenceMediaLimits.Image.MaxDimension = 2048
		m.ReferenceMediaLimits.Image.MinRatio = 0
		m.ReferenceMediaLimits.Image.MaxRatio = 0
		m.Pricing = core.MediaModelPricing{Known: true, ReferencePriceUSD: .112, ReferenceDescription: "One try-on image at published USD balance list price; discounts and actual deductions remain unverified."}
	}
	m.ReferenceMediaLimits.MaxFiles = m.MaxImageInputs
	if id == ApparelReplicator {
		m.ReferenceMediaLimits.MaxFiles += 2
	}
	return m
}
func solutionModels() []core.MediaModel {
	result := []core.MediaModel{}
	for _, id := range []string{KlingEffects, ApparelReplicator, GoodsStudio, VideoCommerce, VirtualTryOn} {
		result = append(result, solutionModel(id))
	}
	return result
}
func solutionInvalid(s string) error { return fmt.Errorf("%w: %s", core.ErrOptionsInvalid, s) }
func solutionProductMode(p map[string]any, productImages int) bool {
	title, _ := p["goods_title"].(string)
	price, _ := p["goods_price"].(string)
	audiences, _ := p["goods_target_audience"].([]string)
	points, _ := p["goods_selling_point"].([]string)
	return productImages > 0 || title != "" || price != "" || len(audiences) > 0 || len(points) > 0
}
func validateSolutionRequest(r core.MediaRequest) error {
	if !isSolutionOperation(r.Model) {
		return core.ErrModelUnavailable
	}
	m, o := solutionModel(r.Model), r.Options
	if (m.NoPrompt && r.Prompt != "") || (!m.NoPrompt && strings.TrimSpace(r.Prompt) == "") || (!m.NoPrompt && utf8.RuneCountInString(r.Prompt) > m.MaxPromptCharacters) {
		return solutionInvalid("invalid Kling solution prompt or product text length")
	}
	if o.OutputCount < 0 || o.OutputCount > 1 || (r.Model != VirtualTryOn && o.OutputCount != 0) || o.Duration != 0 || o.GenerateAudio != nil || o.Seed != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Quality != "" || o.Background != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || len(o.ReferenceVoices) > 0 || o.OverrideSoundID != "" || o.OverrideSound != nil {
		return solutionInvalid("unsupported Kling solution option")
	}
	if o.Resolution != "" && !slices.Contains(m.Resolutions, o.Resolution) {
		return solutionInvalid("unsupported Kling solution resolution")
	}
	if o.Ratio != "" && !slices.Contains(m.Ratios, o.Ratio) {
		return solutionInvalid("unsupported Kling solution aspect ratio")
	}
	if o.Watermark != nil && m.NoWatermark {
		return solutionInvalid("watermark is not a documented effect input")
	}
	if o.Voice != "" && !slices.Contains(m.Voices, o.Voice) {
		return solutionInvalid("unsupported official solution voice")
	}
	if err := core.ValidateReferenceMode(m, o); err != nil {
		return err
	}
	if err := core.ValidateReferenceRoles(m, o); err != nil {
		return err
	}
	if err := core.ValidateResourceReferences(m, o); err != nil {
		return err
	}
	for _, group := range [][]string{o.ImageURLs, o.Videos(), o.Audios()} {
		for _, raw := range group {
			if !publicURL(raw) {
				return solutionInvalid("Kling solution inputs require public HTTPS URLs")
			}
		}
	}
	p, err := core.NormalizeParameters(m.Parameters, o.Parameters)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for i := range o.ImageURLs {
		counts[core.ReferenceRole(m, o, "image", i)]++
	}
	switch r.Model {
	case KlingEffects:
		dual := slices.Contains(dualImageEffects, p["effect_scene"].(string))
		required := 1
		if dual {
			required = 2
		}
		if len(o.ImageURLs) != required {
			return solutionInvalid("effect scene requires its documented single or dual image input")
		}
		for i := range o.ImageURLs {
			role := core.ReferenceRole(m, o, "image", i)
			if role != "reference" && (!dual || role != []string{"left", "right"}[i]) {
				return solutionInvalid("dual effects require left then right image order")
			}
		}
	case ApparelReplicator:
		if counts["product_image"] < 1 || counts["product_image"] > 5 || counts["model_image"] > 5 {
			return solutionInvalid("apparel requires 1–5 product images and at most five model images")
		}
		if seconds, known := o.InputVideoDuration(); known && (seconds < 5 || seconds > 180) {
			return solutionInvalid("apparel source video must be 5–180 seconds")
		}
	case GoodsStudio:
		if o.Resolution == "" || o.Ratio == "" {
			return solutionInvalid("Goods Studio requires resolution and aspect ratio")
		}
	case VideoCommerce:
		for _, key := range []string{"goods_target_audience", "goods_selling_point"} {
			if items, ok := p[key].([]string); ok {
				for _, item := range items {
					if strings.TrimSpace(item) == "" {
						return solutionInvalid("product text lists require non-empty entries")
					}
				}
			}
		}
		avatar, _ := p["avatar_id"].(string)
		if (counts["avatar_image"] == 1) == (avatar != "") || counts["avatar_image"] > 1 || counts["ref_image"] > 5 {
			return solutionInvalid("Video Commerce requires exactly one uploaded or official library avatar")
		}
		if counts["avatar_image"] == 1 && o.Voice == "" {
			return solutionInvalid("uploaded Video Commerce avatars require an official solution voice")
		}
		if avatar != "" && o.Voice != "" {
			return solutionInvalid("library avatars include a voice; voice must be omitted")
		}
		if solutionProductMode(p, counts["ref_image"]) {
			title, _ := p["goods_title"].(string)
			if counts["ref_image"] < 1 || strings.TrimSpace(title) == "" {
				return solutionInvalid("product voiceover requires product image and title")
			}
			if p["speech_rate"] != nil {
				return solutionInvalid("speech rate is unavailable for product voiceover")
			}
		}
	case VirtualTryOn:
		if counts["product_image"] != 1 || counts["person_image"] != 1 {
			return solutionInvalid("try-on requires exactly one product image and one person image")
		}
	}
	return nil
}

// Separate wire structs keep legacy Effects and the unified Solutions protocol
// explicit; no caller map is forwarded to a provider payload.
type solutionContent struct {
	Type    string `json:"type"`
	URL     string `json:"url,omitempty"`
	Text    string `json:"text,omitempty"`
	VoiceID string `json:"voice_id,omitempty"`
}
type solutionSettings struct {
	Resolution     string   `json:"resolution,omitempty"`
	Ratio          string   `json:"aspect_ratio,omitempty"`
	Duration       int      `json:"duration,omitempty"`
	AllowPolish    *bool    `json:"allow_polish,omitempty"`
	BGMEnabled     *bool    `json:"bgm_enabled,omitempty"`
	VoiceID        string   `json:"voice_id,omitempty"`
	SpeechRate     *float64 `json:"speech_rate,omitempty"`
	ActionPrompt   string   `json:"action_prompt,omitempty"`
	KeepFace       *bool    `json:"keep_face,omitempty"`
	KeepPose       *bool    `json:"keep_pose,omitempty"`
	KeepBackground *bool    `json:"keep_background,omitempty"`
}
type solutionOptions struct {
	ExternalTaskID string          `json:"external_task_id,omitempty"`
	Watermark      *imageWatermark `json:"watermark,omitempty"`
	WatermarkInfo  *imageWatermark `json:"watermark_info,omitempty"`
}
type solutionPayload struct {
	Contents []solutionContent `json:"contents"`
	Settings solutionSettings  `json:"settings"`
	Options  solutionOptions   `json:"options"`
}
type effectsPayload struct {
	Scene string `json:"effect_scene"`
	Input struct {
		Image  string   `json:"image,omitempty"`
		Images []string `json:"images,omitempty"`
	} `json:"input"`
	ExternalTaskID string `json:"external_task_id,omitempty"`
}

func solutionNativePayload(r core.MediaRequest) any {
	m, o := solutionModel(r.Model), r.Options
	p, _ := core.NormalizeParameters(m.Parameters, o.Parameters)
	if r.Model == KlingEffects {
		body := effectsPayload{Scene: p["effect_scene"].(string), ExternalTaskID: r.TaskID}
		if len(o.ImageURLs) == 1 {
			body.Input.Image = o.ImageURLs[0]
		} else {
			body.Input.Images = append([]string(nil), o.ImageURLs...)
		}
		return body
	}
	body := solutionPayload{Contents: []solutionContent{}, Options: solutionOptions{ExternalTaskID: r.TaskID}, Settings: solutionSettings{Resolution: o.Resolution, Ratio: o.Ratio}}
	if r.Model != VirtualTryOn && body.Settings.Resolution == "" {
		body.Settings.Resolution = "720p"
	}
	if r.Model == VideoCommerce && body.Settings.Ratio == "" {
		body.Settings.Ratio = "9:16"
	}
	if o.Watermark != nil {
		if r.Model == ApparelReplicator {
			body.Options.WatermarkInfo = &imageWatermark{Enabled: *o.Watermark}
		} else {
			body.Options.Watermark = &imageWatermark{Enabled: *o.Watermark}
		}
	}
	addText := func(kind, text string) {
		body.Contents = append(body.Contents, solutionContent{Type: kind, Text: text})
	}
	switch r.Model {
	case ApparelReplicator:
		addText("product_info", r.Prompt)
	case GoodsStudio:
		addText("goods_title", r.Prompt)
	case VideoCommerce:
		addText("speech_script", r.Prompt)
	}
	for i, raw := range o.ImageURLs {
		body.Contents = append(body.Contents, solutionContent{Type: core.ReferenceRole(m, o, "image", i), URL: raw})
	}
	for i, raw := range o.Videos() {
		body.Contents = append(body.Contents, solutionContent{Type: core.ReferenceRole(m, o, "video", i), URL: raw})
	}
	for i, raw := range o.Audios() {
		body.Contents = append(body.Contents, solutionContent{Type: core.ReferenceRole(m, o, "audio", i), URL: raw})
	}
	if r.Model == ApparelReplicator && o.Voice != "" {
		body.Contents = append(body.Contents, solutionContent{Type: "voice", VoiceID: o.Voice})
	}
	for _, key := range []string{"goods_description", "avatar_id", "goods_title", "goods_price"} {
		if text, ok := p[key].(string); ok && text != "" {
			addText(key, text)
		}
	}
	for _, key := range []string{"goods_target_audience", "goods_selling_point"} {
		if texts, ok := p[key].([]string); ok {
			for _, text := range texts {
				addText(key, text)
			}
		}
	}
	if r.Model == GoodsStudio {
		body.Settings.Duration, _ = strconv.Atoi(p["duration"].(string))
	}
	if r.Model == VideoCommerce {
		body.Settings.VoiceID = o.Voice
		body.Settings.ActionPrompt, _ = p["action_prompt"].(string)
		if raw, ok := p["speech_rate"].(string); ok {
			v, _ := strconv.ParseFloat(raw, 64)
			body.Settings.SpeechRate = &v
		}
		if v, ok := p["allow_polish"].(bool); ok {
			body.Settings.AllowPolish = &v
		}
		if v, ok := p["bgm_enabled"].(bool); ok {
			body.Settings.BGMEnabled = &v
		}
	}
	if r.Model == VirtualTryOn {
		face, pose, background := p["keep_face"].(bool), p["keep_pose"].(bool), p["keep_background"].(bool)
		body.Settings.KeepFace = &face
		body.Settings.KeepPose = &pose
		body.Settings.KeepBackground = &background
	}
	return body
}
func (a *Adapter) generateSolution(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if err := validateSolutionRequest(r); err != nil {
		return core.ProviderTask{}, err
	}
	if r.Model == KlingEffects {
		return a.submitLegacy(ctx, r.Model, solutionPath(r.Model), solutionNativePayload(r))
	}
	if _, err := legacyScope(ctx); err != nil {
		return core.ProviderTask{}, err
	}
	raw, err := a.do(ctx, http.MethodPost, solutionPath(r.Model), solutionNativePayload(r))
	if err != nil {
		return core.ProviderTask{}, err
	}
	var data struct {
		ID     legacyOpaqueID `json:"id"`
		Status string         `json:"status"`
	}
	if json.Unmarshal(raw, &data) != nil || data.ID == "" {
		return core.ProviderTask{}, errors.New("Kling solution submission has no valid task ID; check provider history before resubmitting")
	}
	if data.Status != "submitted" && data.Status != "processing" && data.Status != "succeeded" && !(r.Model == VirtualTryOn && data.Status == "succeed") {
		return core.ProviderTask{}, errors.New("Kling solution submission failed or returned an unknown task status")
	}
	handle, err := a.signLegacy(ctx, r.Model, string(data.ID))
	return core.ProviderTask{ID: handle}, err
}

// Rates are per actual output second. Unknown duration never becomes zero/free.
func solutionRate(r core.MediaRequest) (money.USD, string, error) {
	resolution := r.Options.Resolution
	if resolution == "" {
		resolution = "720p"
	}
	high := resolution == "1080p"
	switch r.Model {
	case ApparelReplicator:
		if r.Options.Voice != "" {
			if high {
				return 280000, "second", nil
			}
			return 238000, "second", nil
		}
		if high {
			return 238000, "second", nil
		}
		return 182000, "second", nil
	case GoodsStudio:
		if high {
			return 280000, "second", nil
		}
		return 238000, "second", nil
	case VideoCommerce:
		m := solutionModel(r.Model)
		n := 0
		for i := range r.Options.ImageURLs {
			if core.ReferenceRole(m, r.Options, "image", i) == "ref_image" {
				n++
			}
		}
		p, _ := core.NormalizeParameters(m.Parameters, r.Options.Parameters)
		if solutionProductMode(p, n) {
			if high {
				return 224000, "second", nil
			}
			return 196000, "second", nil
		}
		if high {
			return 168000, "second", nil
		}
		return 140000, "second", nil
	case VirtualTryOn:
		return 112000, "image", nil
	}
	return 0, "", core.ErrPricingUnavailable
}
func quoteSolution(r core.MediaRequest) (core.TaskCost, string, error) {
	if err := validateSolutionRequest(r); err != nil {
		return core.TaskCost{}, "", err
	}
	rate, unit, err := solutionRate(r)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	quantity := 1.0
	unknown := false
	switch r.Model {
	case ApparelReplicator, VideoCommerce:
		quantity = 0
		unknown = true
	case GoodsStudio:
		p, _ := core.NormalizeParameters(solutionModel(r.Model).Parameters, r.Options.Parameters)
		seconds, _ := strconv.Atoi(p["duration"].(string))
		quantity = float64(seconds)
	}
	amount, err := rate.Multiply(quantity, 1)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	status := "estimated"
	if unknown {
		status = "quantity_unknown"
	}
	return core.TaskCost{QuantityUnknown: unknown, ExpectedUSD: amount, EstimatedUnits: quantity, Status: status, QuotedAt: time.Now().UTC(), Pricing: core.PricingSnapshot{BillInputVideo: priceBool(false), Currency: "USD", Unit: unit, Price: rate, Per: 1, Source: "https://kling.ai/document-api/pricing/solutions/ecommerce", Version: "2026-10-31", Region: "Singapore", CheckedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), EstimateDescription: "Published USD balance list price estimate for requested output; actual duration, account discounts and deductions remain unverified."}}, "", nil
}

type solutionOutput struct {
	Type     string         `json:"type"`
	ID       legacyOpaqueID `json:"id"`
	URL      string         `json:"url"`
	Duration string         `json:"duration"`
}
type solutionTaskData struct {
	ID      legacyOpaqueID   `json:"id"`
	Status  string           `json:"status"`
	Outputs []solutionOutput `json:"outputs"`
	Billing json.RawMessage  `json:"billing"`
}

func (a *Adapter) getSolutionTask(ctx context.Context, operation, id string) (core.ProviderResult, error) {
	if !isSolutionOperation(operation) || !validTaskID(id) {
		return core.ProviderResult{}, errors.New("invalid Kling solution operation or task ID")
	}
	if operation == KlingEffects {
		return a.getEffectsTask(ctx, id)
	}
	raw, err := a.do(ctx, http.MethodGet, "/solutions?"+url.Values{"task_ids": {id}}.Encode(), nil)
	if err != nil {
		return core.ProviderResult{}, err
	}
	var tasks []solutionTaskData
	if json.Unmarshal(raw, &tasks) != nil || len(tasks) != 1 || string(tasks[0].ID) != id {
		return core.ProviderResult{}, errors.New("Kling solution response is malformed or mismatches requested task")
	}
	task := tasks[0]
	switch task.Status {
	case "submitted":
		return core.ProviderResult{Status: core.ProviderQueued}, nil
	case "processing":
		return core.ProviderResult{Status: core.ProviderRunning}, nil
	case "failed":
		return core.ProviderResult{Status: core.ProviderFailed, Error: "Kling solution failed; check provider task details"}, nil
	case "succeeded":
		if operation == VirtualTryOn {
			return core.ProviderResult{}, errors.New("unknown Virtual Try-On status")
		}
	case "succeed":
		if operation != VirtualTryOn {
			return core.ProviderResult{}, errors.New("unknown Kling solution status")
		}
	default:
		return core.ProviderResult{}, errors.New("unknown Kling solution status")
	}
	result := core.ProviderResult{Status: core.ProviderSucceeded}
	duration, images, videos := 0.0, 0, 0
	seen := map[string]bool{}
	for i, out := range task.Outputs {
		kind := ""
		switch operation {
		case ApparelReplicator:
			switch out.Type {
			case "product_image", "model_images":
				kind = "image"
			case "product_video":
				kind = "video"
			}
		case VirtualTryOn:
			if out.Type == "image" {
				kind = "image"
			}
		default:
			if out.Type == "video" {
				kind = "video"
			}
		}
		if kind == "" || !publicURL(out.URL) {
			return core.ProviderResult{}, errors.New("Kling solution returned an unsupported or invalid output")
		}
		seconds := 0.0
		if out.Duration != "" {
			seconds, err = strconv.ParseFloat(out.Duration, 64)
			if err != nil || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
				return core.ProviderResult{}, errors.New("Kling solution returned invalid duration")
			}
		} else if kind == "video" && operation != ApparelReplicator {
			return core.ProviderResult{}, errors.New("Kling solution video has no duration")
		}
		artifactID := string(out.ID)
		if artifactID == "" {
			artifactID = out.Type + ":" + strconv.Itoa(i)
		}
		if seen[artifactID] {
			return core.ProviderResult{}, errors.New("Kling solution returned duplicate artifact IDs")
		}
		seen[artifactID] = true
		result.Artifacts = append(result.Artifacts, core.ProviderArtifact{ID: artifactID, GroupID: out.Type, MediaType: kind, URL: out.URL, Duration: seconds})
		if kind == "video" {
			videos++
			duration += seconds
			if result.ResultURL == "" {
				result.ResultURL = out.URL
			}
		} else {
			images++
		}
	}
	if len(result.Artifacts) == 0 || len(result.Artifacts) > 64 || (operation == VirtualTryOn && videos > 0) || (operation != VirtualTryOn && videos == 0) {
		return core.ProviderResult{}, errors.New("Kling solution task has no valid primary output")
	}
	if result.ResultURL == "" {
		result.ResultURL = result.Artifacts[0].URL
	}
	// The worker's scalar compatibility URL must identify the first artifact.
	// Apparel responses list accompanying images before their primary video.
	for i, artifact := range result.Artifacts {
		if artifact.URL == result.ResultURL && i > 0 {
			primary := artifact
			copy(result.Artifacts[1:i+1], result.Artifacts[:i])
			result.Artifacts[0] = primary
			break
		}
	}

	metrics := []core.UsageMetric{}
	if duration > 0 {
		metrics = append(metrics, core.UsageMetric{Name: "output_video_duration", Unit: "second", Quantity: duration})
	}
	if images > 0 {
		metrics = append(metrics, core.UsageMetric{Name: "generated_images", Unit: "image", Quantity: float64(images)})
	}
	billing, _ := json.Marshal(map[string]json.RawMessage{"billing": task.Billing})
	result.Usage = &core.ProviderUsage{Metrics: metrics, Raw: billing, CostStatus: "metered"}
	return result, nil
}
func (a *Adapter) getEffectsTask(ctx context.Context, id string) (core.ProviderResult, error) {
	raw, err := a.do(ctx, http.MethodGet, "/v1/videos/effects/"+id, nil)
	if err != nil {
		return core.ProviderResult{}, err
	}
	var task struct {
		ID     legacyOpaqueID `json:"task_id"`
		Status string         `json:"task_status"`
		Result struct {
			Videos []solutionOutput `json:"videos"`
		} `json:"task_result"`
		Units   json.RawMessage `json:"final_unit_deduction"`
		Balance json.RawMessage `json:"final_balance_deduction"`
	}
	if json.Unmarshal(raw, &task) != nil || string(task.ID) != id {
		return core.ProviderResult{}, errors.New("malformed Kling effects task response")
	}
	switch task.Status {
	case "submitted":
		return core.ProviderResult{Status: core.ProviderQueued}, nil
	case "processing":
		return core.ProviderResult{Status: core.ProviderRunning}, nil
	case "failed":
		return core.ProviderResult{Status: core.ProviderFailed, Error: "Kling effect failed; check provider task details"}, nil
	case "succeed":
	default:
		return core.ProviderResult{}, errors.New("unknown Kling effects task status")
	}
	result := core.ProviderResult{Status: core.ProviderSucceeded}
	seen := map[string]bool{}
	for _, video := range task.Result.Videos {
		if video.ID == "" || seen[string(video.ID)] || !publicURL(video.URL) {
			return core.ProviderResult{}, errors.New("Kling effect has no valid unique video output")
		}
		seen[string(video.ID)] = true
		result.Artifacts = append(result.Artifacts, core.ProviderArtifact{ID: string(video.ID), MediaType: "video", URL: video.URL, GroupID: "effects"})
	}
	if len(result.Artifacts) == 0 || len(result.Artifacts) > 64 {
		return core.ProviderResult{}, errors.New("Kling effect has an invalid video output count")
	}
	result.ResultURL = result.Artifacts[0].URL
	billing, _ := json.Marshal(map[string]json.RawMessage{"final_unit_deduction": task.Units, "final_balance_deduction": task.Balance})
	result.Usage = &core.ProviderUsage{Raw: billing}
	return result, nil
}
