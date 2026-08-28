-- Rollback remediation persistence tables
DROP TABLE IF EXISTS remediation_tool_invocation;
DROP TABLE IF EXISTS remediation_artifact;
DROP TABLE IF EXISTS remediation_plan;
DROP TABLE IF EXISTS remediation_decision;
DROP TABLE IF EXISTS remediation_run;
DROP TABLE IF EXISTS remediation_series;
