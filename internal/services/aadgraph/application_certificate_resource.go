package aadgraph

import (
	"fmt"
	"log"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/graphrbac/1.6/graphrbac"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"

	"github.com/terraform-providers/terraform-provider-azuread/internal/clients"
	"github.com/terraform-providers/terraform-provider-azuread/internal/services/aadgraph/graph"
	"github.com/terraform-providers/terraform-provider-azuread/internal/tf"
	"github.com/terraform-providers/terraform-provider-azuread/internal/utils"
)

func applicationCertificateResource() *schema.Resource {
	return &schema.Resource{
		Create: applicationCertificateResourceCreate,
		Read:   applicationCertificateResourceRead,
		Delete: applicationCertificateResourceDelete,

		Importer: tf.ValidateResourceIDPriorToImport(func(id string) error {
			_, err := graph.ParseCertificateId(id)
			return err
		}),

		Schema: graph.CertificateResourceSchema("application_object_id"),
	}
}

func applicationCertificateResourceCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.AadClient).AadGraph.ApplicationsClient
	ctx := meta.(*clients.AadClient).StopContext

	objectId := d.Get("application_object_id").(string)

	cred, err := graph.KeyCredentialForResource(d)
	if err != nil {
		return fmt.Errorf("generating certificate credentials for object ID %q: %+v", objectId, err)
	}
	id := graph.CredentialIdFrom(objectId, "certificate", *cred.KeyID)

	tf.LockByName(resourceApplicationName, id.ObjectId)
	defer tf.UnlockByName(resourceApplicationName, id.ObjectId)

	existingCreds, err := client.ListKeyCredentials(ctx, id.ObjectId)
	if err != nil {
		return fmt.Errorf("listing certificate credentials for application with object ID %q: %+v", id.ObjectId, err)
	}

	newCreds, err := graph.KeyCredentialResultAdd(existingCreds, cred)
	if err != nil {
		if _, ok := err.(*graph.AlreadyExistsError); ok {
			return tf.ImportAsExistsError("azuread_application_certificate", id.String())
		}
		return fmt.Errorf("adding Application Certificate: %+v", err)
	}

	if _, err = client.UpdateKeyCredentials(ctx, id.ObjectId, graphrbac.KeyCredentialsUpdateParameters{Value: newCreds}); err != nil {
		return fmt.Errorf("creating certificate credentials %q for application with object ID %q: %+v", id.KeyId, id.ObjectId, err)
	}

	_, err = graph.WaitForKeyCredentialReplication(id.KeyId, d.Timeout(schema.TimeoutCreate), func() (graphrbac.KeyCredentialListResult, error) {
		return client.ListKeyCredentials(ctx, id.ObjectId)
	})
	if err != nil {
		return fmt.Errorf("waiting for certificate credential replication for application (AppID %q, KeyID %q: %+v", id.ObjectId, id.KeyId, err)
	}

	d.SetId(id.String())

	return applicationCertificateResourceRead(d, meta)
}

func applicationCertificateResourceRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.AadClient).AadGraph.ApplicationsClient
	ctx := meta.(*clients.AadClient).StopContext

	id, err := graph.ParseCertificateId(d.Id())
	if err != nil {
		return fmt.Errorf("parsing certificate credential with ID: %v", err)
	}
	// ensure the Application Object exists
	app, err := client.Get(ctx, id.ObjectId)
	if err != nil {
		// the parent Application has been removed - skip it
		if utils.ResponseWasNotFound(app.Response) {
			log.Printf("[DEBUG] Application with Object ID %q was not found - removing from state!", id.ObjectId)
			d.SetId("")
			return nil
		}
		return fmt.Errorf("retrieving application with ID %q: %+v", id.ObjectId, err)
	}

	credentials, err := client.ListKeyCredentials(ctx, id.ObjectId)
	if err != nil {
		return fmt.Errorf("listing certificate credentials for application with object ID %q: %+v", id.ObjectId, err)
	}

	credential := graph.KeyCredentialResultFindByKeyId(credentials, id.KeyId)
	if credential == nil {
		log.Printf("[DEBUG] certificate credential %q (ID %q) was not found - removing from state!", id.KeyId, id.ObjectId)
		d.SetId("")
		return nil
	}

	// todo, move this into a graph helper function?
	d.Set("application_object_id", id.ObjectId)
	d.Set("key_id", id.KeyId)

	if keyType := credential.Type; keyType != nil {
		d.Set("type", keyType)
	}

	if endDate := credential.EndDate; endDate != nil {
		d.Set("end_date", endDate.Format(time.RFC3339))
	}

	if startDate := credential.StartDate; startDate != nil {
		d.Set("start_date", startDate.Format(time.RFC3339))
	}

	return nil
}

func applicationCertificateResourceDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.AadClient).AadGraph.ApplicationsClient
	ctx := meta.(*clients.AadClient).StopContext

	id, err := graph.ParseCertificateId(d.Id())
	if err != nil {
		return fmt.Errorf("parsing certificate credential with ID: %v", err)
	}

	tf.LockByName(resourceApplicationName, id.ObjectId)
	defer tf.UnlockByName(resourceApplicationName, id.ObjectId)

	// ensure the parent Application exists
	app, err := client.Get(ctx, id.ObjectId)
	if err != nil {
		// the parent Application has been removed - skip it
		if utils.ResponseWasNotFound(app.Response) {
			log.Printf("[DEBUG] Application with Object ID %q was not found - removing from state!", id.ObjectId)
			return nil
		}
		return fmt.Errorf("retrieving application with ID %q: %+v", id.ObjectId, err)
	}

	existing, err := client.ListKeyCredentials(ctx, id.ObjectId)
	if err != nil {
		return fmt.Errorf("listing certificate credentials for application %q: %+v", id.ObjectId, err)
	}

	newCreds := graph.KeyCredentialResultRemoveByKeyId(existing, id.KeyId)
	if _, err = client.UpdateKeyCredentials(ctx, id.ObjectId, graphrbac.KeyCredentialsUpdateParameters{Value: newCreds}); err != nil {
		return fmt.Errorf("removing certificate credentials %q from application with object ID %q: %+v", id.KeyId, id.ObjectId, err)
	}

	return nil
}
