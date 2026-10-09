package config

// ErrorDefinition owns a stable public problem description.
type ErrorDefinition struct {
	Code   string `json:"code"`
	Status int    `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}
type ErrorPublication struct {
	Schema        string            `json:"$schema"`
	Title         string            `json:"title"`
	Version       string            `json:"version"`
	Errors        []ErrorDefinition `json:"errors"`
	ProblemFormat string            `json:"problemFormat"`
}

func ErrorDefinitions() ErrorPublication {
	return ErrorPublication{
		Schema:        "https://json-schema.org/draft/2020-12/schema",
		Title:         "Liapoldus Core error catalog",
		Version:       "1.0.0",
		ProblemFormat: "application/problem+json; details contain no secret values, filesystem paths, private keys or raw plugin payloads.",
		Errors: []ErrorDefinition{
			{Code: "invalid_request", Status: 400, Title: "Некорректный запрос", Detail: "Тело запроса отсутствует, повреждено или не соответствует опубликованной схеме."},
			{Code: "bootstrap_invalid", Status: 422, Title: "Некорректная bootstrap-конфигурация", Detail: "Bootstrap core.yaml не соответствует опубликованной схеме."},
			{Code: "unknown_field", Status: 422, Title: "Неизвестное поле", Detail: "Обнаружено поле вне разрешённой bootstrap-схемы."},
			{Code: "plugin_not_found", Status: 404, Title: "Плагин не найден", Detail: "Указанный plugin instance не существует."},
			{Code: "plugin_config_invalid", Status: 422, Title: "Некорректная конфигурация плагина", Detail: "JSON settings не соответствует schema активного Manifest."},
			{Code: "plugin_revision_conflict", Status: 412, Title: "Версия конфигурации устарела", Detail: "If-Match не соответствует текущей revision; active config не изменён."},
			{Code: "plugin_link_conflict", Status: 409, Title: "Политика peer link уже существует", Detail: "Для указанной caller-to-target пары уже есть link policy; используйте replace с If-Match."},
			{Code: "plugin_link_invalid", Status: 422, Title: "Недопустимая политика peer link", Detail: "Набор link rules не соответствует generic placement/carrier/contract-range схеме."},
			{Code: "target_lost", Status: 409, Title: "Целевая replica потеряна", Detail: "Точная incarnation из rollout-когорты истекла или заменена; active generation не откатывается, автоматическая подмена не выполняется."},
			{Code: "idempotency_conflict", Status: 409, Title: "Конфликт ключа идемпотентности", Detail: "Ключ уже использован для запроса с другим digest."},
			{Code: "artifact_invalid", Status: 422, Title: "Недопустимый артефакт", Detail: "Archive повреждён, превышает лимит либо содержит запрещённые записи."},
			{Code: "artifact_too_large", Status: 413, Title: "Артефакт слишком велик", Detail: "Upload превысил один из заданных byte limits."},
			{Code: "activation_failed", Status: 503, Title: "Активация не выполнена", Detail: "Candidate snapshot не активирован; прежний runtime и pointers сохранены."},
			{Code: "management_bearer_required", Status: 401, Title: "Требуется service key", Detail: "Management API требует действующий Bearer service key."},
			{Code: "management_unavailable", Status: 503, Title: "Management API недоступен", Detail: "Не удалось выполнить операцию с постоянным состоянием Management API."},
			{Code: "forbidden", Status: 403, Title: "Доступ запрещён", Detail: "Для операции требуется platform-admin."},
			{Code: "plugin_unavailable", Status: 503, Title: "Плагин недоступен", Detail: "Подключённый plugin instance не отвечает."},
			{Code: "plugin_tls_rejected", Status: 502, Title: "TLS плагина отклонён", Detail: "Plugin peer не прошёл проверку TLS identity."},
			{Code: "resource_exhausted", Status: 503, Title: "Исчерпан ресурс", Detail: "Операция превышает доступную concurrency или storage quota."},
		},
	}
}
