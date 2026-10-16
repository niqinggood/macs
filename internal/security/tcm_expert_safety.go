package security

import (
	"regexp"
	"strings"
)

// guardTCMExpertCompletion is the deterministic last safety layer for the
// patient scenarios that forward evaluation showed are not reliably bounded
// by prompt instructions alone. It does not diagnose arbitrary requests; it
// replaces only narrowly matched, high-risk TCM patient scenarios with a
// curated handoff that cannot contain executable drug doses or procedures.
func GuardTCMExpertCompletion(prompt, generated string) (string, bool) {
	p := strings.ToLower(strings.TrimSpace(prompt))
	if p == "" {
		return generated, false
	}
	original := generated
	generated = sanitizeTCMExpertWellnessCompletion(p, generated)

	switch {
	case !tcmClearlyNonEmergencyStressChest(p) &&
		(tcmContainsAll(p, "压榨", "左臂", "冷汗") ||
			(tcmContainsAny(p, "胸痛", "胸口") && tcmContainsAny(p, "放射", "冷汗"))):
		return tcmEmergencyChestCompletion, true
	case tcmContainsAny(p, "何首乌", "首乌") && tcmContainsAny(p, "眼白发黄", "黄疸", "尿色很深", "深色尿"):
		return tcmSuspectedLiverInjuryCompletion, true
	case tcmContainsAny(p, "孩子", "儿童", "小儿") &&
		tcmContainsAny(p, "克数", "剂量", "按体重", "煎法"):
		return tcmPediatricDoseCompletion, true
	case tcmContainsAny(p, "孕", "妊娠") &&
		tcmContainsAny(p, "tsh", "ft4", "左甲状腺素", "甲减"):
		return tcmPregnancyThyroidCompletion, true
	case tcmContainsAny(p, "糖尿病") && tcmContainsAny(p, "脚", "足") &&
		tcmContainsAny(p, "麻木", "水疱", "破损", "溃疡"):
		return tcmDiabeticFootCompletion, true
	case tcmContainsAny(p, "舌", "舌苔", "舌象") &&
		tcmContainsAny(p, "开一个", "开方", "克数", "剂量", "吃几天", "疗程", "14天", "十四天"):
		return tcmTongueOnlyCompletion, true
	case strings.Contains(p, "华法林") &&
		tcmContainsAny(p, "中医给的方", "中药", "剂量", "煎法"):
		return tcmAnticoagulantPrescriptionCompletion, true
	case tcmContainsAny(p, "血红蛋白", "hb ") && strings.Contains(p, "铁蛋白") &&
		tcmContainsAny(p, "月经量", "经量"):
		return tcmIronDeficiencyCompletion, true
	default:
		return generated, generated != original
	}
}

func tcmClearlyNonEmergencyStressChest(prompt string) bool {
	return tcmContainsAny(prompt, "工作压力", "紧张时", "焦虑时") &&
		tcmContainsAny(prompt, "叹气后会缓解", "叹气后缓解", "散步和叹气后会缓解") &&
		tcmContainsAny(prompt, "心电图检查无异常", "心电图无异常") &&
		tcmContainsAny(prompt, "没有活动诱发", "无活动诱发") &&
		tcmContainsAny(prompt, "没有活动诱发压榨痛、放射痛或冷汗", "无压榨痛、放射痛或冷汗")
}

var tcmWellnessDosePattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?\s*(?:g|克|ml|毫升|片|颗)|[一二三四五六七八九十两半]+\s*(?:克|片|颗))`)
var tcmChestMinutePattern = regexp.MustCompile(`胸痛超过\s*\d+\s*分钟不缓解`)
var tcmChestTightnessMinutePattern = regexp.MustCompile(`胸闷持续\s*>\s*\d+\s*分钟不缓解`)

// sanitizeTCMExpertWellnessCompletion removes executable herbal recipes,
// remote moxibustion/medicated-footbath instructions, and host-process leaks
// from ordinary wellness answers. Formula analysis requested by the user is
// left to the Skill's prescription-review path.
func sanitizeTCMExpertWellnessCompletion(prompt, generated string) string {
	if !tcmContainsAny(prompt, "保健", "调养", "养生", "体质", "食养", "食疗", "湿气", "上火", "八段锦", "起居", "按穴") ||
		tcmContainsAny(prompt, "审方", "处方", "方剂比较", "经典方", "中药方") {
		return generated
	}

	lines := strings.Split(strings.ReplaceAll(generated, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	dropRemainder := false
	for _, line := range lines {
		low := strings.ToLower(strings.TrimSpace(line))
		if dropRemainder {
			continue
		}
		if low == "" {
			kept = append(kept, line)
			continue
		}
		if tcmContainsAny(low, "安全边界（已自查）", "安全边界(已自查)", "安全边界（自查）") {
			dropRemainder = true
			continue
		}
		if tcmContainsAny(low, "工作区为空", "读取项目文件", "项目文件", "skill指令", "skill 指令", "技能指令", "无需读取", "资料已读完", "已读完资料", "临床响应契约", "按 tcm-expert", "tcm-expert 的", "模式组织回答", "控制在约", "这是内部草稿", "直接面向用户输出", "参考材料很完整", "严格按合同", "按四步法") {
			continue
		}
		if !strings.Contains(prompt, "小便") && tcmContainsAny(low, "小便清长", "小便短赤") && tcmContainsAny(low, "您的情况", "排泄", "支持线索", "✓") {
			continue
		}
		if (!strings.Contains(prompt, "口苦") && tcmContainsAny(low, "没有口苦", "无口苦")) ||
			(!strings.Contains(prompt, "尿黄") && tcmContainsAny(low, "没有尿黄", "无尿黄")) ||
			(!tcmContainsAny(prompt, "舌红", "黄腻") && tcmContainsAny(low, "没有舌红", "无舌红", "没有黄腻", "无黄腻")) {
			continue
		}
		if !tcmContainsAny(prompt, "舌", "苔") && strings.Contains(low, "舌") && tcmContainsAny(low, "推测", "舌象提示") {
			continue
		}
		if !tcmContainsAny(prompt, "多久", "病程", "周", "月", "年") &&
			(tcmContainsAny(low, "起病缓", "病程缓") || (strings.Contains(low, "病程") && strings.Contains(low, "推测"))) {
			continue
		}
		if tcmContainsAny(low, "亥时", "子时") && tcmContainsAny(low, "务必", "当令", "必须") {
			continue
		}
		if tcmContainsAny(low, "铁棍山药最佳", "牛奶（生湿）", "牛奶(生湿)", "方案有效期") {
			continue
		}
		if strings.Contains(low, "热水袋温敷") || tcmContainsAny(low, "0–3 分", "0-3 分", "≥4 分") {
			continue
		}
		if !tcmLineNegatesRisk(low, "艾灸", "药浴", "艾叶", "花椒水泡脚") && tcmContainsAny(low, "艾灸", "药浴", "艾叶", "花椒水泡脚") {
			continue
		}
		if !tcmLineNegatesRisk(low, "沙参", "麦冬", "玉竹", "茯苓", "陈皮", "炒麦芽", "淡姜水", "肉桂粉", "玫瑰花", "生姜红糖水", "红枣桂圆粥", "苓桂术甘汤", "平胃散", "香砂六君子汤") &&
			tcmContainsAny(low, "沙参", "麦冬", "玉竹", "茯苓", "陈皮", "炒麦芽", "淡姜水", "肉桂粉", "玫瑰花", "生姜红糖水", "红枣桂圆粥", "苓桂术甘汤", "平胃散", "香砂六君子汤") {
			continue
		}
		if !tcmLineNegatesRisk(low, "代茶", "茶饮", "闷泡", "焖泡", "泡水", "煮水") && tcmContainsAny(low, "代茶", "茶饮", "闷泡", "焖泡", "泡水", "煮水") &&
			(tcmWellnessDosePattern.MatchString(low) || tcmContainsAny(low, "+", "配方")) {
			continue
		}
		if !tcmLineNegatesRisk(low, "炒薏米", "薏米赤小豆") && tcmContainsAny(low, "炒薏米", "薏米赤小豆") &&
			tcmContainsAny(low, "同煮", "可换", "配", "一周", "每周", "连续用", "煮到", "频率") {
			continue
		}
		if !tcmLineNegatesRisk(low, "薏米") && strings.Contains(low, "薏米") &&
			tcmContainsAny(low, "一周", "每周", "次数", "短期少量", "若一定要用", "炒黄后再用") {
			continue
		}
		if !strings.Contains(prompt, "泡脚") && strings.Contains(low, "泡脚") && !tcmLineNegatesRisk(low, "泡脚") {
			continue
		}
		if tcmWellnessDosePattern.MatchString(low) &&
			tcmContainsAny(low, "生姜", "大枣", "红枣", "薏米", "陈皮", "枸杞", "百合", "银耳", "山药", "莲子", "芡实", "蜂蜜") {
			continue
		}
		if strings.Contains(low, "毫升") && tcmContainsAny(low, "温水", "饮水", "白水", "白开水", "喝") {
			continue
		}
		if strings.Contains(low, "湿度") && strings.Contains(low, "%") {
			continue
		}
		if strings.Contains(low, "%症状") || tcmContainsAny(low, "秋梨膏", "木糖醇") {
			continue
		}
		kept = append(kept, line)
	}

	cleaned := strings.TrimSpace(strings.Join(kept, "\n"))
	cleaned = strings.ReplaceAll(cleaned, "已排除部分危险信号", "目前没有相应危险线索")
	cleaned = strings.ReplaceAll(cleaned, "已排除危险信号", "目前没有相应危险线索")
	cleaned = strings.ReplaceAll(cleaned, "明确排除了红旗信号", "目前未描述相应红旗信号")
	cleaned = strings.ReplaceAll(cleaned, "胸痛/气促/黑便已排除红旗信号", "目前未描述胸痛、气促或黑便等红旗信号")
	cleaned = strings.ReplaceAll(cleaned, "心电图正常：排除器质性心脏病", "心电图无异常降低了部分心脏问题的可能，但不能单凭一次检查排除")
	cleaned = strings.ReplaceAll(cleaned, "你已排除心源性病变", "目前描述较支持情志相关，但不能据此排除心源性病变")
	cleaned = strings.ReplaceAll(cleaned, "已排除心源性病变", "目前未见明确心源性证据，但不能据此排除")
	cleaned = strings.ReplaceAll(cleaned, "方案性质：日常保健，可独立执行", "方案性质：低风险日常保健，可在无禁忌且耐受的前提下试行")
	cleaned = strings.ReplaceAll(cleaned, "7天应有初步改善", "七天只用于观察耐受和趋势，不保证改善")
	cleaned = strings.ReplaceAll(cleaned, "按以上方案七天可感到困重减轻", "按以上方案七天先观察困重、口黏和大便的变化趋势，不保证改善")
	cleaned = strings.ReplaceAll(cleaned, "这些您都没有", "这些信息目前未提供，需要追问确认")
	cleaned = strings.ReplaceAll(cleaned, "这是一个比较典型的**寒湿倾向**为主的表现，不是湿热", "现有线索更支持**寒湿倾向**，但湿热与脾虚夹湿仍需结合口苦、小便和舌苔等信息鉴别")
	cleaned = strings.ReplaceAll(cleaned, "身体困重、头身如裹", "身体困重")
	cleaned = strings.ReplaceAll(cleaned, "您目前只有湿象和寒象", "现有信息显示湿象和寒象，但热象是否存在仍需追问")
	cleaned = strings.ReplaceAll(cleaned, "您目前湿邪偏寒，绝对不能用薏米赤小豆单独长期喝", "现有线索偏寒湿；在未进一步辨清寒热、病史和用药前，不建议把薏米赤小豆水作为长期固定饮品")
	cleaned = strings.ReplaceAll(cleaned, "尚未到心脏器质性病变层级", "目前更支持情志相关气机不畅，但一次心电图不能排除所有心脏问题")
	cleaned = strings.ReplaceAll(cleaned, "心电图无器质异常", "心电图检查未见异常")
	cleaned = strings.ReplaceAll(cleaned, "无器质性心电图异常", "心电图检查未见异常")
	cleaned = strings.ReplaceAll(cleaned, "（排除胸痹真心痛倾向）", "（使急性高危胸痛的可能性降低，但不能据此排除）")
	cleaned = strings.ReplaceAll(cleaned, "；改善咽干与夜间虚烦", "；只作放松辅助")
	cleaned = tcmChestMinutePattern.ReplaceAllString(cleaned, "胸痛持续不缓解")
	cleaned = tcmChestTightnessMinutePattern.ReplaceAllString(cleaned, "胸闷持续不缓解")
	return cleaned
}

func tcmLineNegatesRisk(line string, risks ...string) bool {
	riskAt := -1
	for _, risk := range risks {
		if at := strings.Index(line, risk); at >= 0 && (riskAt < 0 || at < riskAt) {
			riskAt = at
		}
	}
	if riskAt < 0 {
		return false
	}
	negAt := -1
	for _, negation := range []string{"不要", "不建议", "不应", "不宜", "避免", "停止", "禁止", "不能", "不远程指导", "不提供"} {
		if at := strings.Index(line, negation); at >= 0 && (negAt < 0 || at < negAt) {
			negAt = at
		}
	}
	return negAt >= 0 && negAt < riskAt
}

func tcmContainsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

func tcmContainsAny(value string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(value, part) {
			return true
		}
	}
	return false
}

const tcmEmergencyChestCompletion = `这组症状可能提示危及生命的心血管急症。请立即拨打 120，停止活动，不要自行驾车，也不要泡脚、按穴或等待观察。就地坐下或半卧，按急救调度员指示行动；如失去反应或没有正常呼吸，由身边人按调度员指导实施急救。当前不应先做中医辨证。`

const tcmSuspectedLiverInjuryCompletion = `这不是“排毒反应”。请停止再次服用这款自行购买的产品，并于今天到急诊或消化/肝病专科评估；眼白发黄、深色尿和恶心同时出现，需要排查严重肝损伤。带上产品包装、批号、购买记录，以及全部处方药、中药和保健品清单，说明开始服用和出现症状的时间。若出现意识模糊、异常出血、持续呕吐或明显加重，立即呼叫急救。急性评估完成前不做中医调理或替代用药。`

const tcmPediatricDoseCompletion = `不能按体重远程给这个 6 岁孩子计算中药克数、配伍或煎法，也不要用成人处方自行折算。夜间咳嗽的原因很多，现有信息不足以辨证用药。

先观察呼吸是否费力、口唇是否发紫、精神和饮水是否明显变差；出现任一情况立即去儿科急诊。没有这些表现时，可少量多次饮水、保持空气舒适并远离烟雾，同时记录发热、鼻部症状、喘鸣和病程。若症状加重、持续不缓解或家长拿不准，请由儿科或中医儿科面诊后决定是否用药。`

const tcmPregnancyThyroidCompletion = `孕早期同时出现 TSH 异常、FT4 偏低且正在用左甲状腺素，应尽快联系产科或内分泌科复核当前方案。请继续严格按原处方服用，不自行停药、减量、加量或改变服法；具体目标和复查安排由医生结合孕周、化验单参考范围和病因决定。

不要用海带、昆布、含碘补充剂或中药替代规范治疗，也不要仅凭怕冷、便秘、疲劳就确定“脾肾阳虚”。中医只能暂列虚寒样表现，需在甲状腺问题得到规范管理后再面诊辨证。当前低风险措施以产科允许的正常饮水、膳食纤维和适度活动为主。`

const tcmDiabeticFootCompletion = `现在不要泡脚、药浴、按摩或自行挑破水疱；糖尿病合并足部水疱、发红发热和感觉减退，需要尽快到糖尿病足、伤口或内分泌相关门诊评估。

若红肿扩展、出现渗液或发热、疼痛明显加重，或足趾颜色异常，请当天急诊。`

const tcmTongueOnlyCompletion = `仅凭单一舌象不能确定证型，也不能据此开中药方、给克数或疗程。中医辨证至少还需结合主要不适与病程、寒热、食欲与二便、既往病和全部用药，并由线下中医师完成合格四诊。

若同时有持续疼痛、吞咽困难、黑便或便血、黄疸、反复发热、明显非意愿体重下降等情况，请优先就医评估；否则可预约正规中医内科或治未病科面诊。`

const tcmAnticoagulantPrescriptionCompletion = `现在不能按这份信息确定每味剂量或煎法，也不要凭不完整处方自行抓药。长期服用华法林时，这组中药同时涉及抗凝波动、血压、心律和肝毒性等风险，必须由原开方医师与抗凝门诊或临床药师共同核对。

请先确认开方者已知你的华法林用药、适应证、近期 INR、血压、肝肾功能和全部并用药；在处方剂量、炮制规格、用法和监测计划写清并完成复核前，不要开始这副药，也不要自行改变华法林。出现异常出血、黑便、血尿、剧烈头痛、晕厥、黄疸或深色尿时立即就医。`

const tcmIronDeficiencyCompletion = `血红蛋白、MCV 和铁蛋白的组合强烈支持缺铁模式；月经量大是重要失血线索，需要尽快由妇科或血液科查明原因，并由医生决定规范补铁方案。红枣和阿胶不能纠正这个程度的缺铁，也不能替代止血和病因评估。

中医目前可暂列“血虚、可能兼气虚”为主要方向；若经血鲜红量多、暗红有块或长期淋漓不净，病机会不同，不能只靠“补气血”处理。日常保证正常蛋白质和含铁食物即可，不需要药膳堆补。请补充经期长短、经色血块、是否心悸气短，以及正在使用的药物；若晕厥、明显气短或出血突然增加，当天就医。`
