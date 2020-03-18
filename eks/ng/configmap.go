package ng

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"text/template"
	"time"

	"github.com/aws/aws-k8s-tester/pkg/fileutil"
	"go.uber.org/zap"
	"k8s.io/utils/exec"
)

func (ts *tester) createConfigMap() error {
	ts.cfg.Logger.Info("writing ConfigMap", zap.String("instance-role-arn", ts.cfg.EKSConfig.AddOnNodeGroups.RoleARN))
	p, err := writeConfigMapAuth(ts.cfg.EKSConfig.AddOnNodeGroups.RoleARN)
	if err != nil {
		return err
	}

	// might take several minutes for DNS to propagate
	waitDur := 5 * time.Minute
	retryStart := time.Now()
	for time.Now().Sub(retryStart) < waitDur {
		select {
		case <-ts.cfg.Stopc:
			return errors.New("create ConfigMap aborted")
		case <-ts.cfg.Sig:
			return errors.New("create ConfigMap aborted")
		case <-time.After(5 * time.Second):
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		output, err := exec.New().CommandContext(
			ctx,
			ts.cfg.EKSConfig.KubectlPath,
			"--kubeconfig="+ts.cfg.EKSConfig.KubeConfigPath,
			"apply", "--filename="+p,
		).CombinedOutput()
		cancel()
		out := string(output)
		if err != nil {
			return fmt.Errorf("'kubectl version' failed %v (output %q)", err, out)
		}
		fmt.Printf("\n\"kubectl version\" output:\n%s\n", out)

		ts.cfg.Logger.Warn("create ConfigMap failed", zap.Error(err))
		ts.cfg.EKSConfig.RecordStatus(fmt.Sprintf("create ConfigMap failed (%v)", err))
	}
	ts.cfg.Logger.Info("created ConfigMap")

	return ts.cfg.EKSConfig.Sync()
}

// https://docs.aws.amazon.com/eks/latest/userguide/getting-started.html
const configMapAuthTempl = `---
apiVersion: v1
kind: ConfigMap
metadata:
  name: aws-auth
  namespace: kube-system
data:
  mapRoles: |
    - rolearn: {{.NGInstanceRoleARN}}
      %s
      groups:
      - system:bootstrappers
      - system:nodes
`

type configMapAuth struct {
	NGInstanceRoleARN string
}

func writeConfigMapAuth(arn string) (p string, err error) {
	kc := configMapAuth{NGInstanceRoleARN: arn}
	tpl := template.Must(template.New("configMapAuthTempl").Parse(configMapAuthTempl))
	buf := bytes.NewBuffer(nil)
	if err = tpl.Execute(buf, kc); err != nil {
		return "", err
	}
	// avoid '{{' conflicts with Go
	txt := fmt.Sprintf(buf.String(), `username: system:node:{{EC2PrivateDNSName}}`)
	return fileutil.WriteTempFile([]byte(txt))
}
