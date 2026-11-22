package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"gorm.io/gorm"
)

type ProductCategoriesHandler struct {
	db *gorm.DB
}

func NewProductCategoriesHandler(db *gorm.DB) *ProductCategoriesHandler {
	return &ProductCategoriesHandler{db: db}
}

func (h *ProductCategoriesHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /product-categories", h.list)
	mux.HandleFunc("POST /product-categories", h.create)
	mux.HandleFunc("PUT /product-categories/{id}", h.update)
	mux.HandleFunc("DELETE /product-categories/{id}", h.delete)
}

type productCategoryRequest struct {
	Name string `json:"name"`
}

const duplicateCategoryMsg = "já existe uma categoria com esse nome"

func (h *ProductCategoriesHandler) list(w http.ResponseWriter, r *http.Request) {
	categories := []models.ProductCategory{}
	if err := h.db.WithContext(r.Context()).Order("lower(name) asc, name asc").Find(&categories).Error; err != nil {
		log.Printf("ERROR ListProductCategories: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, categories)
}

func (h *ProductCategoriesHandler) nameTaken(ctx context.Context, name string, exceptID *uuid.UUID) (bool, error) {
	q := h.db.WithContext(ctx).Model(&models.ProductCategory{}).Where("lower(name) = lower(?)", name)
	if exceptID != nil {
		q = q.Where("id <> ?", *exceptID)
	}
	var n int64
	err := q.Count(&n).Error
	return n > 0, err
}

func (h *ProductCategoriesHandler) create(w http.ResponseWriter, r *http.Request) {
	var req productCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	name, msg := normalizeName("name", req.Name)
	if msg != "" {
		models.WriteError(w, http.StatusBadRequest, msg)
		return
	}

	taken, err := h.nameTaken(r.Context(), name, nil)
	if err != nil {
		log.Printf("ERROR CreateProductCategory check: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if taken {
		models.WriteError(w, http.StatusConflict, duplicateCategoryMsg)
		return
	}

	c := models.ProductCategory{Name: name}
	if err := h.db.WithContext(r.Context()).Create(&c).Error; err != nil {
		if isUniqueViolation(err) {
			models.WriteError(w, http.StatusConflict, duplicateCategoryMsg)
			return
		}
		log.Printf("ERROR CreateProductCategory: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *ProductCategoriesHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var req productCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	name, msg := normalizeName("name", req.Name)
	if msg != "" {
		models.WriteError(w, http.StatusBadRequest, msg)
		return
	}

	var c models.ProductCategory
	if err := h.db.WithContext(r.Context()).First(&c, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "categoria não encontrada")
			return
		}
		log.Printf("ERROR UpdateProductCategory find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	taken, err := h.nameTaken(r.Context(), name, &c.ID)
	if err != nil {
		log.Printf("ERROR UpdateProductCategory check: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if taken {
		models.WriteError(w, http.StatusConflict, duplicateCategoryMsg)
		return
	}

	c.Name = name
	if err := h.db.WithContext(r.Context()).Save(&c).Error; err != nil {
		if isUniqueViolation(err) {
			models.WriteError(w, http.StatusConflict, duplicateCategoryMsg)
			return
		}
		log.Printf("ERROR UpdateProductCategory save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *ProductCategoriesHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	res := h.db.WithContext(r.Context()).Delete(&models.ProductCategory{}, "id = ?", id)
	if res.Error != nil {
		log.Printf("ERROR SoftDeleteProductCategory: %v", res.Error)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if res.RowsAffected == 0 {
		models.WriteError(w, http.StatusNotFound, "categoria não encontrada")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
