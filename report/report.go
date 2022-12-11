package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"zuri.chat/zccore/utils"
)

// Add a report.
func (rh *Handler) AddReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var report Report

	err := json.NewDecoder(r.Body).Decode(&report)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	orgID := mux.Vars(r)["id"]

	if strings.Contains(orgID, "-org") {
		orgDoc, _ := utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": orgID})

		if orgDoc == nil {
			utils.GetError(errors.New("organization with id "+orgID+" doesn't exist!"), http.StatusBadRequest, w)
			return
		}
	} else {

		objID, err := primitive.ObjectIDFromHex(orgID)

		if err != nil {
			utils.GetError(errors.New("invalid id"), http.StatusBadRequest, w)
			return
		}

		orgDoc, _ := utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": objID})

		if orgDoc == nil {
			utils.GetError(errors.New("organization with id "+orgID+" doesn't exist!"), http.StatusBadRequest, w)
			return
		}
	}

	report.Organization = orgID
	report.Date = time.Now()

	if !utils.IsValidEmail(report.ReporterEmail) {
		utils.GetError(fmt.Errorf("invalid email format : %s", report.ReporterEmail), http.StatusBadRequest, w)
		return
	}

	// check that reporter is in the organization
	reporterDoc, _ := utils.GetMongoDBDoc(MemberCollectionName, bson.M{"org_id": orgID, "email": report.ReporterEmail})
	if reporterDoc == nil {
		utils.GetError(errors.New("reporter must be a member of this organization"), http.StatusBadRequest, w)
		return
	}

	if !utils.IsValidEmail(report.OffenderEmail) {
		utils.GetError(fmt.Errorf("invalid email format : %s", report.OffenderEmail), http.StatusBadRequest, w)
		return
	}

	// check that offender is in the organization
	offenderDoc, _ := utils.GetMongoDBDoc(MemberCollectionName, bson.M{"org_id": orgID, "email": report.OffenderEmail})
	if offenderDoc == nil {
		utils.GetError(errors.New("offender must be a member of this organization"), http.StatusBadRequest, w)
		return
	}

	if report.Organization == "" {
		utils.GetError(errors.New("organization id required"), http.StatusBadRequest, w)
		return
	}

	if report.Subject == "" {
		utils.GetError(errors.New("report's subject required"), http.StatusBadRequest, w)
		return
	}

	if report.Body == "" {
		utils.GetError(errors.New("report's body required"), http.StatusBadRequest, w)
		return
	}

	var reportMap map[string]interface{}
	reportMap, err = utils.StructToMap(report)

	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	save, err := utils.CreateMongoDBDoc(ReportCollectionName, reportMap)
	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	utils.GetSuccess("report created", utils.M{"report_id": save.InsertedID}, w)
}

// Get a report.
func (rh *Handler) GetReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	orgID := mux.Vars(r)["id"]

	reportID := mux.Vars(r)["report_id"]
	reportObjID, err := primitive.ObjectIDFromHex(reportID)

	if err != nil {
		utils.GetError(errors.New("invalid id"), http.StatusBadRequest, w)
		return
	}

	doc, _ := utils.GetMongoDBDoc(ReportCollectionName, bson.M{"organization_id": orgID, "_id": reportObjID})

	if doc == nil {
		utils.GetError(fmt.Errorf("report %s not found", orgID), http.StatusNotFound, w)
		return
	}

	var report Report
	err = utils.BsonToStruct(doc, &report)

	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	utils.GetSuccess("report retrieved successfully", report, w)
}

// Get reports.
func (rh *Handler) GetReports(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	orgID := mux.Vars(r)["id"]

	docs, _ := utils.GetMongoDBDocs(ReportCollectionName, bson.M{"organization_id": orgID})

	reports := []Report{}

	if docs == nil {
		utils.GetSuccess("no report has been added yet", reports, w)
		return
	}

	for _, doc := range docs {
		var report Report
		err := utils.BsonToStruct(doc, &report)

		if err != nil {
			utils.GetError(err, http.StatusInternalServerError, w)
			return
		}

		reports = append(reports, report)
	}

	utils.GetSuccess("reports retrieved successfully", reports, w)
}
