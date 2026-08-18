# The chat system prompt is a deployment-wide singleton, so the import ID is
# required by the CLI syntax but otherwise unused.
terraform import coderd_chat_system_prompt.this chat_system_prompt
