# 方法来源与网上 Skill 调研

本文件记录设计依据，不是当前市场价表。不要从这些资料推导某地区的实时人天价格。

## 权威方法来源

- [GAO Cost Estimating and Assessment Guide (GAO-20-195G)](https://www.gao.gov/assets/d20195G.pdf)：技术基线、WBS、数据质量、假设、敏感性、风险/不确定性、分位预算和防止风险重复计算。
- [COCOMO II web model / SoftwareCost.org](https://www.softwarecost.org/tools/COCOMO/)：软件规模、尺度因子和成本驱动的参数化估算框架；本 Skill 只在规模和参数有依据时用作交叉检查。
- [COSMIC Method introduction](https://cosmic-sizing.org/cosmic-sizing/intro/)：以 Entry、Exit、Read、Write 数据移动度量技术无关的功能规模。
- [COSMIC guide for non-functional requirements](https://cosmic-sizing.org/publications/non-functional-requirements-and-cosmic-sizing-practitioners-guide/)：说明功能规模与 NFR 分配/估算的关系，避免只数页面或接口。
- [AWS Well-Architected: Load test your workload](https://docs.aws.amazon.com/wellarchitected/latest/framework/perf_process_culture_load_test.html)：在生产相似环境按真实流量场景验证吞吐、响应和瓶颈，并持续复测。
- [Google SRE production service best practices](https://sre.google/sre-book/service-best-practices/)：容量预测、负载测试、冗余、过载降级、限流和重试等生产化成本驱动。
- [Google SRE launch checklist](https://sre.google/sre-book/launch-checklist/)：流量混合、带宽、存储、容灾、监控、安全、发布和增长检查项。
- [Google Rules of Machine Learning](https://developers.google.com/machine-learning/guides/rules-of-ml/)：把数据、流水线、基线、离线/在线评估、训练与服务一致性纳入算法系统范围。
- [Google Research: The ML Test Score](https://research.google/pubs/the-ml-test-score-a-rubric-for-ml-production-readiness-and-technical-debt-reduction/)：生产 ML 的测试与监控不是模型训练之外的可忽略工作。
- [Google Cloud MLOps automation pipelines](https://docs.cloud.google.com/architecture/mlops-continuous-delivery-and-automation-pipelines-in-machine-learning)：区分手工实验、训练流水线自动化、数据/模型验证、持续训练与部署。
- [NIST AI RMF Core](https://airc.nist.gov/airmf-resources/airmf/5-sec-core/)：AI 系统全生命周期的 Govern、Map、Measure、Manage 风险活动，用于高风险 AI 的治理与评估范围。

## Agent Skill 参考

调研于 2026-09-01，通过 GitHub 代码搜索读取公开 `SKILL.md`：

- [mlamp/cost-estimate](https://github.com/mlamp/cost-estimate/blob/13797dc9ea4a7a449fd67249a41dc52f84bae2ce/skill/SKILL.md)：代码库重建成本、简化 COCOMO、多个估算锚点和不确定性披露。其目标是“已存在代码库的重建成本”，不能直接代替售前需求报价。
- [maythyai/code-skills-package / csp-effort-estimation](https://github.com/maythyai/code-skills-package/blob/42a7677c518a1fb4a9ccfcec1dd902828184a64e/csp-workflow/skills/csp-effort-estimation/SKILL.md)：WBS、PERT、团队/技术熟悉度、资源与时间线工作流。示例校正系数没有直接复制到本 Skill。
- [aj-geddes/useful-ai-prompts / project-estimation](https://github.com/aj-geddes/useful-ai-prompts/blob/3f5182cfd739fc113f4af5244a1cf342ad7f7911/skills/project-estimation/SKILL.md)：自下而上、类比和多方法交叉检查的简洁 Skill 结构。
- [ztuotuoz/ps-agent / ps-apaas-evaluator](https://github.com/ztuotuoz/ps-agent/blob/1f6c28ad6c1d475486e43f05424c067999a6a0ff/ps-apaas-evaluator/SKILL.md)：从需求材料到模块、工作项、工时、风险和表格交付的领域化流程。

GitHub 搜索还发现多个“工时估算/项目估算”技能，但没有一个同时覆盖小程序、H5、管理后台、后端、并发容量、算法研发/MLOps和商业价格构成。因此本 Skill 采用组合设计，并避免复制来源中的任意行业费率、固定缓冲比例或未经校准的生产率。

## 使用这些来源的边界

- 来源支持方法和检查项，不支持具体项目金额；
- 公开 Skill 的示例数字不能作为本项目基线；
- 标准化规模方法需要组织历史生产率才能转换为可靠人日；
- COCOMO、PERT、功能规模和类比结果是估算视角，不是实际成本证明；
- 现行税、云服务、模型 API、短信、地图和市场人天价格必须在报价时按地区与日期重新核验。
