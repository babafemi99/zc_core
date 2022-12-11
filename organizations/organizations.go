package organizations

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	"zuri.chat/zccore/SuidService"

	"github.com/go-playground/validator/v10"
	"github.com/gorilla/mux"
	"github.com/mitchellh/mapstructure"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"zuri.chat/zccore/auth"
	"zuri.chat/zccore/logger"
	"zuri.chat/zccore/service"
	"zuri.chat/zccore/user"
	"zuri.chat/zccore/utils"
)

// Get an organization record.
func (oh *OrganizationHandler) GetOrganization(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	objID := mux.Vars(r)["id"]

	if objID == "" {
		utils.GetError(errors.New("invalid id"), http.StatusBadRequest, w)
		return
	}

	if !strings.Contains(objID, "-org") {

		objID, err := primitive.ObjectIDFromHex(objID)
		if err != nil {
			utils.GetError(errors.New("invalid id"), http.StatusBadRequest, w)
			return
		}

		save, _ := utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": objID})

		if save == nil {
			utils.GetError(fmt.Errorf("organization %s not found", objID), http.StatusNotFound, w)
			return
		}

		var org Organization
		// convert bson to struct
		bsonBytes, _ := bson.Marshal(save)

		err = bson.Unmarshal(bsonBytes, &org)
		if err != nil {
			utils.GetError(err, http.StatusInternalServerError, w)
			return
		}

		org.Plugins = org.OrgPlugins()

		utils.GetSuccess("organization retrieved successfully", org, w)

		return
	}

	save, _ := utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": objID})

	if save == nil {
		utils.GetError(fmt.Errorf("organization %s not found", objID), http.StatusNotFound, w)
		return
	}

	var org Organization
	// convert bson to struct
	bsonBytes, _ := bson.Marshal(save)

	err := bson.Unmarshal(bsonBytes, &org)
	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	org.Plugins = org.OrgPlugins()

	utils.GetSuccess("organization retrieved successfully", org, w)
}

// Get an organization by url.
func (oh *OrganizationHandler) GetOrganizationByURL(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	orgURL := mux.Vars(r)["url"]
	data, err := utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"workspace_url": orgURL})

	if data == nil {
		logger.Error("workspace with url %s doesn't exist!", orgURL)
		utils.GetError(errors.New("organization does not exist"), http.StatusNotFound, w)

		return
	}

	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	var org Organization

	orgJSON, _ := json.Marshal(data)
	if err = json.Unmarshal(orgJSON, &org); err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	org.Plugins = org.OrgPlugins()

	utils.GetSuccess("organization retrieved successfully", org, w)
}

// Create an organization record.
func (oh *OrganizationHandler) Create(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var newOrg Organization

	if r.Body == nil {
		utils.GetError(fmt.Errorf("missing body request"), http.StatusBadRequest, w)
		return
	}

	err := json.NewDecoder(r.Body).Decode(&newOrg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// validate that email is not empty and it meets the format
	if !utils.IsValidEmail(newOrg.CreatorEmail) {
		utils.GetError(fmt.Errorf("invalid email format : %s", newOrg.CreatorEmail), http.StatusBadRequest, w)
		return
	}

	// generate workspace url
	//newOrg.Name = "Zuri Chat"
	newOrg.WorkspaceURL = utils.GenWorkspaceURL(newOrg.Name)

	userEmail := strings.ToLower(newOrg.CreatorEmail)
	userName := strings.Split(userEmail, "@")[0]

	// get creator id
	creator, _ := auth.FetchUserByEmail(bson.M{"email": userEmail})

	userDoc, _ := utils.GetMongoDBDoc(UserCollectionName, bson.M{"email": newOrg.CreatorEmail})
	if userDoc == nil {
		utils.GetError(errors.New("user with this email does not exist"), http.StatusBadRequest, w)

		return
	}

	newOrg.CreatorID = creator.ID
	newOrg.CreatorEmail = userEmail
	newOrg.CreatedAt = time.Now()

	newOrg.Plugins = map[string]interface{}{}

	// initialize organization with 100 free tokens
	newOrg.Tokens = 100
	newOrg.Version = FreeVersion
	suid := SuidService.NewSuid()

	if len(newOrg.Name) >= 10 {
		newOrg.Name = newOrg.Name[:10]
	}

	str := fmt.Sprintf("%s-org", newOrg.Name)
	nn := strings.ReplaceAll(str, " ", "-")
	orgID := suid.GenerateId(nn, 5)
	newOrg.ID = orgID

	// convert to map object
	var inInterface map[string]interface{}

	inrec, _ := json.Marshal(newOrg)
	err = json.Unmarshal(inrec, &inInterface)

	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	// save organization
	save, err := utils.CreateMongoDBDoc(OrganizationCollectionName, inInterface)
	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	//iid := save.InsertedID
	//iiid := iid.(primitive.ObjectID).Hex()

	// Adding user as a member
	var userObj user.User
	if err = mapstructure.Decode(userDoc, &userObj); err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	newMember := NewMember(userObj.Email, userName, orgID, OwnerRole)

	// add new member to member collection
	coll := utils.GetCollection(MemberCollectionName)
	if _, err = coll.InsertOne(r.Context(), newMember); err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	// add organisation id to user organisations list
	updateFields := make(map[string]interface{})

	userObj.Organizations = append(userObj.Organizations, orgID)

	updateFields["workspaces"] = userObj.Organizations
	_, ee := utils.UpdateOneMongoDBDoc(UserCollectionName, orgID, updateFields)

	if ee != nil {
		utils.GetError(errors.New("user update failed"), http.StatusInternalServerError, w)
		return
	}

	utils.GetSuccess("organization created", utils.M{"organization_id": save.InsertedID}, w)
}

// Get all organization records.
func (oh *OrganizationHandler) GetOrganizations(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	save, err := utils.GetMongoDBDocs(OrganizationCollectionName, nil)
	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	utils.GetSuccess("organizations retrieved successfully", save, w)
}

// Delete an organization record.
func (oh *OrganizationHandler) DeleteOrganization(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	log.Println("in here 1")
	orgID := mux.Vars(r)["id"]

	response, err := utils.DeleteOneMongoDBDoc(OrganizationCollectionName, orgID)

	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	if response.DeletedCount == 0 {
		utils.GetError(errors.New("operation failed"), http.StatusInternalServerError, w)
		return
	}
	filter := bson.M{"org_id": orgID}
	response1, err2 := utils.DeleteManyMongoDBDoc(MemberCollectionName,filter)

	if err2 != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	if response1.DeletedCount == 0 {
		utils.GetError(errors.New("operation failed"), http.StatusInternalServerError, w)
		return
	}

	utils.GetSuccess("organization deleted successfully", nil, w)
}

// Update an organization workspace url.
func (oh *OrganizationHandler) UpdateURL(w http.ResponseWriter, r *http.Request) {
	OrganizationUpdate(w, r, updateParam{
		orgFilterKey:   "workspace_url",
		requestDataKey: "url",
		eventKey:       UpdateOrganizationName,
		successMessage: "organization url",
	})
}

// Update organization name.
func (oh *OrganizationHandler) UpdateName(w http.ResponseWriter, r *http.Request) {
	OrganizationUpdate(w, r, updateParam{
		orgFilterKey:   "name",
		requestDataKey: "organization_name",
		eventKey:       UpdateOrganizationName,
		successMessage: "organization name",
	})
}

// Transfer workspace ownership.
func (oh *OrganizationHandler) TransferOwnership(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	orgID := mux.Vars(r)["id"]

	// Checks if organization id is valid
	orgIDHex, err := primitive.ObjectIDFromHex(orgID)
	if err != nil {
		utils.GetError(errors.New("invalid organization id"), http.StatusBadRequest, w)
		return
	}

	// Checks if organization exists in the database
	orgDoc, _ := utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": orgIDHex})
	if orgDoc == nil {
		utils.GetError(errors.New("organization does not exist"), http.StatusBadRequest, w)
		return
	}

	requestData := make(map[string]string)
	if err = utils.ParseJSONFromRequest(r, &requestData); err != nil {
		utils.GetError(err, http.StatusUnprocessableEntity, w)
		return
	}

	// email is that of the proposed new owner
	email := requestData["email"]

	// confirms if email supplied is valid
	if !utils.IsValidEmail(strings.ToLower(email)) {
		utils.GetError(errors.New("email is not valid"), http.StatusBadRequest, w)
		return
	}

	// fetches the details of the proposed new owner patterned after member's struct
	orgMember, err := FetchMember(bson.M{"org_id": orgID, "email": email})

	if err != nil {
		utils.GetError(errors.New("user not a member of this work space"), http.StatusBadRequest, w)
		return
	}

	// checks if proposed owner does not have an ownership status already
	if orgMember.Role == "owner" {
		utils.GetError(errors.New("this member already owns this organization"), http.StatusBadRequest, w)
		return
	}

	// member ID of the proposed new owner
	memberID := orgMember.ID

	// upgrades status from member to owner
	updateRes, err := utils.UpdateOneMongoDBDoc(MemberCollectionName, memberID, bson.M{"role": OwnerRole})

	if err != nil {
		utils.GetError(errors.New("operation failed"), http.StatusInternalServerError, w)
		return
	}

	if updateRes.ModifiedCount == 0 {
		utils.GetError(errors.New("could not upgrade member's role"), http.StatusInternalServerError, w)
		return
	}

	// fetches details of the former owner so we can get keys to downgrade status to member
	// checks like isOwner and memberExists are not made since auth.IsAuthorized function already
	// this user pass marks

	loggedInUser, ok := r.Context().Value("user").(*auth.AuthUser)
	if !ok {
		utils.GetError(errors.New("invalid user"), http.StatusBadRequest, w)
		return
	}

	formerOwner, _ := FetchMember(bson.M{"org_id": orgID, "email": loggedInUser.Email})

	// ID of former owner
	formerOwnerID := formerOwner.ID

	// role downgraded from owner to member
	update, err := utils.UpdateOneMongoDBDoc(MemberCollectionName, formerOwnerID, bson.M{"role": AdminRole})

	if err != nil {
		utils.GetError(errors.New("operation failed"), http.StatusInternalServerError, w)
		return
	}

	if update.ModifiedCount == 0 {
		utils.GetError(errors.New("could not downgrade owner's role"), http.StatusInternalServerError, w)
		return
	}

	// and we are done!!!
	utils.GetSuccess("workspace owner changed successfully", nil, w)
}

// Update organization logo.
func (oh *OrganizationHandler) UpdateLogo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-Type", "application/json")

	orgID := mux.Vars(r)["id"]

	// check that org_id is valid
	err := ValidateOrg(orgID)
	if err != nil {
		utils.GetError(err, http.StatusBadRequest, w)
		return
	}

	uploadPath := "logo/" + orgID

	imgURL, err := service.ProfileImageUpload(uploadPath, logoWidth, logoHeight, r)
	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	update, err := utils.UpdateOneMongoDBDoc(OrganizationCollectionName, orgID, bson.M{"logo_url": imgURL})

	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	if update.ModifiedCount == 0 {
		utils.GetError(errors.New("operation failed"), http.StatusInternalServerError, w)
		return
	}

	eventChannel := fmt.Sprintf("organizations_%s", orgID)
	event := utils.Event{Identifier: orgID, Type: "Organization", Event: UpdateOrganizationLogo, Channel: eventChannel, Payload: make(map[string]interface{})}

	go utils.Emitter(event)

	utils.GetSuccess("Logo updated successfully", imgURL, w)
}

// Send invite to a list of emails.
func (oh *OrganizationHandler) SendInvite(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	loggedInUser, ok := r.Context().Value("user").(*auth.AuthUser)
	if !ok {
		utils.GetError(errors.New("invalid user"), http.StatusBadRequest, w)
		return
	}

	sOrgID := mux.Vars(r)["id"]

	var guests SendInviteBody
	var org bson.M

	err := utils.ParseJSONFromRequest(r, &guests)
	if err != nil {
		utils.GetError(err, http.StatusUnprocessableEntity, w)
		return
	}

	if strings.Contains(sOrgID, "-org") {
		// get previous settings
		org, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": sOrgID})
		if org == nil {
			utils.GetError(fmt.Errorf("organization %s not found", sOrgID), http.StatusNotFound, w)
			return
		}
	} else {
		// get previous settings 2
		objID, err := primitive.ObjectIDFromHex(sOrgID)
		if err != nil {
			utils.GetError(err, http.StatusUnprocessableEntity, w)
			return
		}

		org, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": objID})
		if org == nil {
			utils.GetError(fmt.Errorf("organization %s not found", sOrgID), http.StatusNotFound, w)
			return
		}
	}

	var invalidEmails []interface{}

	inviteIDs := make([]interface{}, len(guests.Emails))

	for _, email := range guests.Emails {
		// Check the validity of email send
		if !utils.IsValidEmail(email) {
			// If Email is invalid append to list to invalid emails
			invalidEmails = append(invalidEmails, email)
			continue
		}
		// Generate new UUI for invite and
		uuid := utils.GenUUID()

		newInvite := Invite{OrgID: sOrgID, UUID: uuid, Email: email, HasAccepted: false}

		var invInterface map[string]interface{}

		inrec, _ := json.Marshal(newInvite)
		err = json.Unmarshal(inrec, &invInterface)

		if err != nil {
			utils.GetError(err, http.StatusInternalServerError, w)
			return
		}

		// Save newly generated uuid and associated info in the database
		save, err := utils.CreateMongoDBDoc(OrganizationInviteCollectionName, invInterface)
		if err != nil {
			utils.GetError(err, http.StatusInternalServerError, w)

			return
		}

		// Append new invite to array of generated invites
		inviteIDs = append(inviteIDs, save.InsertedID)

		// Parse data for customising email template

		inviteLink := fmt.Sprintf("%s/%s", os.Getenv("INVITE_DOMAIN"), uuid)
		orgName := fmt.Sprintf("%v", org["name"])

		msger := oh.mailService.NewMail(
			[]string{email}, "Zuri Chat Workspace Invite", service.WorkSpaceInvite, map[string]interface{}{
				"Username":   loggedInUser.Email,
				"OrgName":    orgName,
				"InviteLink": inviteLink,
			})
		// error with sending main
		if err := oh.mailService.SendMail(msger); err != nil {
			logger.Error("Error occurred while sending mail: %s", err.Error())
		}
	}

	response := SendInviteResponse{InvalidEmails: invalidEmails, InviteIDs: inviteIDs}

	utils.GetSuccess("Organization invite operation result", response, w)
}

// Get invite records of an organization.
func (oh *OrganizationHandler) InviteStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	orgID := mux.Vars(r)["id"]

	invites, err := utils.GetMongoDBDocs(OrganizationInviteCollectionName, bson.M{"org_id": orgID})
	if err != nil {
		utils.GetError(err, http.StatusNotFound, w)
	}

	utils.GetSuccess("successful", invites, w)
}

// Upgrade services to Pro.
func (oh *OrganizationHandler) UpgradeToPro(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	orgID := mux.Vars(r)["id"]

	// check whether organization is already pro member
	isPro, err := IsProVersion(orgID)
	if err != nil {
		utils.GetError(err, http.StatusNotAcceptable, w)
		return
	}

	if isPro {
		utils.GetError(errors.New("organisation already on pro version"), http.StatusBadRequest, w)
		return
	}

	if err = SubscriptionBilling(orgID, float64(ProSubscriptionRate)); err != nil {
		utils.GetError(err, http.StatusExpectationFailed, w)
	}

	updateData := make(map[string]interface{})
	updateData["version"] = ProVersion

	update, err := utils.UpdateOneMongoDBDoc(OrganizationCollectionName, orgID, updateData)
	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	if update.ModifiedCount == 0 {
		utils.GetError(errors.New("operation failed"), http.StatusInternalServerError, w)
		return
	}

	utils.GetSuccess("Organization successfully updated to pro", nil, w)
}

func IsProVersion(orgID string) (bool, error) {

	if strings.Contains(orgID, "-org") {
		organization, err := FetchOrganization(bson.M{"_id": orgID})
		if err != nil {
			return false, err
		}
		return organization.Version == ProVersion, nil
	} else {
		OrgIDFromHex, err := primitive.ObjectIDFromHex(orgID)
		if err != nil {
			return false, err
		}
		organization, err := FetchOrganization(bson.M{"_id": OrgIDFromHex})
		if err != nil {
			return false, err
		}
		return organization.Version == ProVersion, nil
	}

}

// Update an organization billing settings.
func (oh *OrganizationHandler) UpdateBillingSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var billingSetting BillingSetting

	payload := settingsPayload{
		settings: &billingSetting,
		field:    "billing.setting",
	}

	updateBilling(w, r, payload)
}

// Update an organization billing contact.
func (oh *OrganizationHandler) UpdateBillingContact(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var billingContact BillingContact

	payload := settingsPayload{
		settings: &billingContact,
		field:    "billing.contact",
	}

	updateBilling(w, r, payload)
}

// Update an organization settings.
func (oh *OrganizationHandler) UpdateOrganizationSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	orgID := mux.Vars(r)["id"]

	var orgSettings OrgSettings

	var save bson.M

	err := utils.ParseJSONFromRequest(r, &orgSettings)
	if err != nil {
		utils.GetError(err, http.StatusUnprocessableEntity, w)
		return
	}

	if strings.Contains(orgID, "-org") {
		save, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": orgID})
		if save == nil {
			utils.GetError(fmt.Errorf("organization %s not found", orgID), http.StatusNotFound, w)
			return
		}
	} else {
		objID, err := primitive.ObjectIDFromHex(orgID)
		if err != nil {
			utils.GetError(err, http.StatusUnprocessableEntity, w)
			return
		}

		save, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": objID})
		if save == nil {
			utils.GetError(fmt.Errorf("organization %s not found", orgID), http.StatusNotFound, w)
			return
		}
	}

	validate := validator.New()

	var org Organization

	// convert bson to struct
	bsonBytes, _ := bson.Marshal(&save)

	err = bson.Unmarshal(bsonBytes, &org)
	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	// valdate struct
	if err = validate.Struct(org); err != nil {
		utils.GetError(err, http.StatusBadRequest, w)
		return
	}
	// adds new settings with existing settings
	orgPref := OrganizationPreference{
		orgSettings,
		org.Settings.Permissions,
		org.Settings.Authentication,
	}

	orgFilter := make(map[string]interface{})
	orgFilter["settings"] = orgPref

	update, err := utils.UpdateOneMongoDBDoc(OrganizationCollectionName, orgID, orgFilter)
	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	if update.ModifiedCount == 0 {
		utils.GetError(errors.New("operation failed"), http.StatusUnprocessableEntity, w)
		return
	}

	utils.GetSuccess("organization settings updated successfully", nil, w)
}

// Update an organization permission settings.
func (oh *OrganizationHandler) UpdateOrganizationPermission(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	orgID := mux.Vars(r)["id"]

	var orgPermissions OrgPermissions
	var save bson.M

	err := utils.ParseJSONFromRequest(r, &orgPermissions)
	if err != nil {
		utils.GetError(err, http.StatusUnprocessableEntity, w)
		return
	}

	if strings.Contains(orgID, "-org") {
		// get previous settings
		save, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": orgID})
		if save == nil {
			utils.GetError(fmt.Errorf("organization %s not found", orgID), http.StatusNotFound, w)
			return
		}
	} else {
		// get previous settings 2
		objID, err := primitive.ObjectIDFromHex(orgID)
		if err != nil {
			utils.GetError(err, http.StatusUnprocessableEntity, w)
			return
		}

		save, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": objID})
		if save == nil {
			utils.GetError(fmt.Errorf("organization %s not found", orgID), http.StatusNotFound, w)
			return
		}
	}

	validate := validator.New()

	var org Organization

	// convert bson to struct
	bsonBytes, _ := bson.Marshal(&save)
	err = bson.Unmarshal(bsonBytes, &org)

	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	// valdate struct
	if err = validate.Struct(org); err != nil {
		utils.GetError(err, http.StatusBadRequest, w)
		return
	}

	// adds new settings with existing settings
	orgPref := OrganizationPreference{
		org.Settings.Settings,
		orgPermissions,
		org.Settings.Authentication,
	}

	orgFilter := make(map[string]interface{})
	orgFilter["settings"] = orgPref

	update, err := utils.UpdateOneMongoDBDoc(OrganizationCollectionName, orgID, orgFilter)
	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	if update.ModifiedCount == 0 {
		utils.GetError(errors.New("operation failed"), http.StatusUnprocessableEntity, w)
		return
	}

	utils.GetSuccess("organization settings updated successfully", nil, w)
}

// Update an organization authentication settings.
func (oh *OrganizationHandler) UpdateOrganizationAuthentication(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	orgID := mux.Vars(r)["id"]

	var orgAuthentication OrgAuthentication
	var save bson.M

	err := utils.ParseJSONFromRequest(r, &orgAuthentication)
	if err != nil {
		utils.GetError(err, http.StatusUnprocessableEntity, w)
		return
	}

	if strings.Contains(orgID, "-org") {
		// get previous settings
		save, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": orgID})
		if save == nil {
			utils.GetError(fmt.Errorf("organization %s not found", orgID), http.StatusNotFound, w)
			return
		}
	} else {
		// get previous settings 2
		objID, err := primitive.ObjectIDFromHex(orgID)
		if err != nil {
			utils.GetError(err, http.StatusUnprocessableEntity, w)
			return
		}

		save, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": objID})
		if save == nil {
			utils.GetError(fmt.Errorf("organization %s not found", orgID), http.StatusNotFound, w)
			return
		}
	}

	validate := validator.New()
	var org Organization

	// convert bson to struct
	bsonBytes, _ := bson.Marshal(&save)
	err = bson.Unmarshal(bsonBytes, &org)

	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	// valdate struct
	if err = validate.Struct(org); err != nil {
		utils.GetError(err, http.StatusBadRequest, w)
		return
	}
	// adds new settings with existing settings
	orgPref := OrganizationPreference{
		org.Settings.Settings,
		org.Settings.Permissions,
		orgAuthentication,
	}

	orgFilter := make(map[string]interface{})
	orgFilter["settings"] = orgPref

	update, err := utils.UpdateOneMongoDBDoc(OrganizationCollectionName, orgID, orgFilter)
	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	if update.ModifiedCount == 0 {
		utils.GetError(errors.New("operation failed"), http.StatusUnprocessableEntity, w)
		return
	}

	utils.GetSuccess("organization settings updated successfully", nil, w)
}

// Update an organization channel prefix.
func (oh *OrganizationHandler) UpdateOrganizationPrefixes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	orgID := mux.Vars(r)["id"]

	var channelprefixes ChannelPrefixes
	var save bson.M

	err := utils.ParseJSONFromRequest(r, &channelprefixes)
	if err != nil {
		utils.GetError(err, http.StatusUnprocessableEntity, w)
		return
	}

	if strings.Contains(orgID, "-org") {
		// get previous settings
		save, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": orgID})
		if save == nil {
			utils.GetError(fmt.Errorf("organization %s not found", orgID), http.StatusNotFound, w)
			return
		}
	} else {
		// get previous settings 2
		objID, err := primitive.ObjectIDFromHex(orgID)
		if err != nil {
			utils.GetError(err, http.StatusUnprocessableEntity, w)
			return
		}

		save, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": objID})
		if save == nil {
			utils.GetError(fmt.Errorf("organization %s not found", orgID), http.StatusNotFound, w)
			return
		}
	}

	validate := validator.New()
	var org Organization

	// convert bson to struct
	bsonBytes, _ := bson.Marshal(save)
	err = bson.Unmarshal(bsonBytes, &org)

	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	// valdate struct
	if err = validate.Struct(org); err != nil {
		utils.GetError(err, http.StatusBadRequest, w)
		return
	}
	// adds new prefixes with existing settings
	orgPref := Customize{
		append(org.Customize.Prefixes, channelprefixes),
		org.Customize.AddCustomEmoji,
		org.Customize.SlackBot,
	}

	orgFilter := make(map[string]interface{})
	orgFilter["customize"] = orgPref

	update, err := utils.UpdateOneMongoDBDoc(OrganizationCollectionName, orgID, orgFilter)
	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	if update.ModifiedCount == 0 {
		utils.GetError(errors.New("operation failed"), http.StatusUnprocessableEntity, w)
		return
	}

	utils.GetSuccess("organization channelprefixes updated successfully", nil, w)
}

// Update an organization slackbot response.
func (oh *OrganizationHandler) UpdateSlackBotResponses(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	orgID := mux.Vars(r)["id"]

	var slackbotresponse SlackBot
	var save bson.M

	err := utils.ParseJSONFromRequest(r, &slackbotresponse)
	if err != nil {
		utils.GetError(err, http.StatusUnprocessableEntity, w)
		return
	}

	if strings.Contains(orgID, "-org") {
		// get previous settings
		save, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": orgID})
		if save == nil {
			utils.GetError(fmt.Errorf("organization %s not found", orgID), http.StatusNotFound, w)
			return
		}
	} else {
		// get previous settings 2
		objID, err := primitive.ObjectIDFromHex(orgID)
		if err != nil {
			utils.GetError(err, http.StatusUnprocessableEntity, w)
			return
		}

		save, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": objID})
		if save == nil {
			utils.GetError(fmt.Errorf("organization %s not found", orgID), http.StatusNotFound, w)
			return
		}
	}

	validate := validator.New()
	var org Organization

	// convert bson to struct
	bsonBytes, _ := bson.Marshal(save)
	err = bson.Unmarshal(bsonBytes, &org)

	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	// validate struct
	if err = validate.Struct(org); err != nil {
		utils.GetError(err, http.StatusBadRequest, w)
		return
	}

	orgPref := Customize{
		org.Customize.Prefixes,
		org.Customize.AddCustomEmoji,
		append(org.Customize.SlackBot, slackbotresponse),
	}

	orgFilter := make(map[string]interface{})
	orgFilter["customize"] = orgPref

	update, err := utils.UpdateOneMongoDBDoc(OrganizationCollectionName, orgID, orgFilter)
	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	if update.ModifiedCount == 0 {
		utils.GetError(errors.New("operation failed"), http.StatusUnprocessableEntity, w)
		return
	}

	utils.GetSuccess("organization slackbotresponse updated successfully", nil, w)
}

func (oh *OrganizationHandler) AddSlackCustomEmoji(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	orgID := mux.Vars(r)["id"]

	var customemoji CustomEmoji
	var save bson.M

	err := utils.ParseJSONFromRequest(r, &customemoji)
	if err != nil {
		utils.GetError(err, http.StatusUnprocessableEntity, w)
		return
	}

	if strings.Contains(orgID, "-org") {
		// get previous settings
		save, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": orgID})
		if save == nil {
			utils.GetError(fmt.Errorf("organization %s not found", orgID), http.StatusNotFound, w)
			return
		}
	} else {
		// get previous settings 2
		objID, err := primitive.ObjectIDFromHex(orgID)
		if err != nil {
			utils.GetError(err, http.StatusUnprocessableEntity, w)
			return
		}

		save, _ = utils.GetMongoDBDoc(OrganizationCollectionName, bson.M{"_id": objID})
		if save == nil {
			utils.GetError(fmt.Errorf("organization %s not found", orgID), http.StatusNotFound, w)
			return
		}
	}

	validate := validator.New()
	var org Organization

	// convert bson to struct
	bsonBytes, _ := bson.Marshal(&save)
	err = bson.Unmarshal(bsonBytes, &org)

	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	// valdate struct
	if err = validate.Struct(org); err != nil {
		utils.GetError(err, http.StatusBadRequest, w)
		return
	}

	orgPref := Customize{
		org.Customize.Prefixes,
		append(org.Customize.AddCustomEmoji, customemoji),
		org.Customize.SlackBot,
	}

	orgFilter := make(map[string]interface{})
	orgFilter["customize"] = orgPref

	update, err := utils.UpdateOneMongoDBDoc(OrganizationCollectionName, orgID, orgFilter)
	if err != nil {
		utils.GetError(err, http.StatusInternalServerError, w)
		return
	}

	if update.ModifiedCount == 0 {
		utils.GetError(errors.New("operation failed"), http.StatusUnprocessableEntity, w)
		return
	}

	utils.GetSuccess("organization customemoji updated successfully", nil, w)
}
