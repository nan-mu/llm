package control

import (
	"context"
	"database/sql"
	"errors"

	"encore.dev/beta/errs"
	"encore.dev/storage/sqldb"
)

// PurposeTranslationContract is the singleton row for purpose=translation.
type PurposeTranslationContract struct {
	ContentEncoding          string  `json:"content_encoding"`
	AllowSystemRole          bool    `json:"allow_system_role"`
	AllowMultimodalContent   bool    `json:"allow_multimodal_content"`
	TemperatureDefault       float64 `json:"temperature_default"`
	TemperatureMax           float64 `json:"temperature_max"`
	TopPDefault              float64 `json:"top_p_default"`
	TopPMax                  float64 `json:"top_p_max"`
	TopKDefault              int     `json:"top_k_default"`
	TopKMax                  int     `json:"top_k_max"`
	RepetitionPenaltyDefault float64 `json:"repetition_penalty_default"`
	RepetitionPenaltyMax     float64 `json:"repetition_penalty_max"`
	MaxTokensDefault         int     `json:"max_tokens_default"`
	MaxTokensMax             int     `json:"max_tokens_max"`
	InjectTopK               bool    `json:"inject_top_k"`
	InjectRepetitionPenalty  bool    `json:"inject_repetition_penalty"`
}

// PurposeStructuredTranslationContract is the singleton row for purpose=structured_translation.
type PurposeStructuredTranslationContract struct {
	AllowedRoles          []string `json:"allowed_roles"`
	DenySystemRole        bool     `json:"deny_system_role"`
	UserContentKind       string   `json:"user_content_kind"`
	UserContentLen        int      `json:"user_content_len"`
	AllowTypeText         bool     `json:"allow_type_text"`
	AllowTypeImage        bool     `json:"allow_type_image"`
	RequireSourceLangCode bool     `json:"require_source_lang_code"`
	RequireTargetLangCode bool     `json:"require_target_lang_code"`
	TextPayloadField      string   `json:"text_payload_field"`
	ImagePayloadField     string   `json:"image_payload_field"`
	LangCodePattern       string   `json:"lang_code_pattern"`
	SupportedLangCodes    []string `json:"supported_lang_codes,omitempty"`
	TemperatureDefault    *float64 `json:"temperature_default,omitempty"`
	TemperatureMax        *float64 `json:"temperature_max,omitempty"`
	MaxTokensDefault      *int     `json:"max_tokens_default,omitempty"`
	MaxTokensMax          *int     `json:"max_tokens_max,omitempty"`
	RequireChatTemplate   bool     `json:"require_chat_template"`
	ChatTemplateID        string   `json:"chat_template_id"`
}

// PurposeASRContract is the singleton row for purpose=asr.
// Transcriptions are not implemented on the gateway yet.
type PurposeASRContract struct {
	Route string `json:"route"`
}

// GetPurposeTranslation returns the translation purpose contract.
//
//encore:api private method=GET path=/control/purpose/translation
func (s *Service) GetPurposeTranslation(ctx context.Context) (*PurposeTranslationContract, error) {
	var c PurposeTranslationContract
	err := db.QueryRow(ctx, `
		SELECT content_encoding, allow_system_role, allow_multimodal_content,
		       temperature_default, temperature_max,
		       top_p_default, top_p_max,
		       top_k_default, top_k_max,
		       repetition_penalty_default, repetition_penalty_max,
		       max_tokens_default, max_tokens_max,
		       inject_top_k, inject_repetition_penalty
		FROM purpose_translation WHERE id = 1
	`).Scan(
		&c.ContentEncoding, &c.AllowSystemRole, &c.AllowMultimodalContent,
		&c.TemperatureDefault, &c.TemperatureMax,
		&c.TopPDefault, &c.TopPMax,
		&c.TopKDefault, &c.TopKMax,
		&c.RepetitionPenaltyDefault, &c.RepetitionPenaltyMax,
		&c.MaxTokensDefault, &c.MaxTokensMax,
		&c.InjectTopK, &c.InjectRepetitionPenalty,
	)
	if errors.Is(err, sqldb.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: "purpose_translation not seeded"}
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetPurposeStructuredTranslation returns the structured_translation contract.
//
//encore:api private method=GET path=/control/purpose/structured_translation
func (s *Service) GetPurposeStructuredTranslation(ctx context.Context) (*PurposeStructuredTranslationContract, error) {
	var c PurposeStructuredTranslationContract
	var tempDef, tempMax sql.NullFloat64
	var maxDef, maxMax sql.NullInt64
	err := db.QueryRow(ctx, `
		SELECT allowed_roles, deny_system_role, user_content_kind, user_content_len,
		       allow_type_text, allow_type_image,
		       require_source_lang_code, require_target_lang_code,
		       text_payload_field, image_payload_field, lang_code_pattern,
		       COALESCE(supported_lang_codes, '{}'),
		       temperature_default, temperature_max,
		       max_tokens_default, max_tokens_max,
		       require_chat_template, chat_template_id
		FROM purpose_structured_translation WHERE id = 1
	`).Scan(
		&c.AllowedRoles, &c.DenySystemRole, &c.UserContentKind, &c.UserContentLen,
		&c.AllowTypeText, &c.AllowTypeImage,
		&c.RequireSourceLangCode, &c.RequireTargetLangCode,
		&c.TextPayloadField, &c.ImagePayloadField, &c.LangCodePattern,
		&c.SupportedLangCodes,
		&tempDef, &tempMax,
		&maxDef, &maxMax,
		&c.RequireChatTemplate, &c.ChatTemplateID,
	)
	if errors.Is(err, sqldb.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: "purpose_structured_translation not seeded"}
	}
	if err != nil {
		return nil, err
	}
	if tempDef.Valid {
		v := tempDef.Float64
		c.TemperatureDefault = &v
	}
	if tempMax.Valid {
		v := tempMax.Float64
		c.TemperatureMax = &v
	}
	if maxDef.Valid {
		v := int(maxDef.Int64)
		c.MaxTokensDefault = &v
	}
	if maxMax.Valid {
		v := int(maxMax.Int64)
		c.MaxTokensMax = &v
	}
	return &c, nil
}

// GetPurposeASR returns the ASR purpose contract.
//
//encore:api private method=GET path=/control/purpose/asr
func (s *Service) GetPurposeASR(ctx context.Context) (*PurposeASRContract, error) {
	var c PurposeASRContract
	err := db.QueryRow(ctx, `SELECT route FROM purpose_asr WHERE id = 1`).Scan(&c.Route)
	if errors.Is(err, sqldb.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: "purpose_asr not seeded"}
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}
