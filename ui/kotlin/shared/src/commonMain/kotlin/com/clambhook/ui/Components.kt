// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.FlowRowScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.MenuAnchorType
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.PrimaryScrollableTabRow
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

/** Minimum touch/click target for every interactive control. */
val MinTarget = 48.dp

@Composable
fun PrimaryAction(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    description: String? = null,
) {
    Button(
        onClick = onClick,
        enabled = enabled,
        modifier = modifier.heightIn(min = MinTarget)
            .semantics { if (description != null) contentDescription = description },
    ) { Text(text) }
}

@Composable
fun SecondaryAction(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    description: String? = null,
) {
    OutlinedButton(
        onClick = onClick,
        enabled = enabled,
        modifier = modifier.heightIn(min = MinTarget)
            .semantics { if (description != null) contentDescription = description },
    ) { Text(text) }
}

@Composable
fun LinkAction(text: String, description: String, enabled: Boolean, onClick: () -> Unit) {
    TextButton(
        onClick = onClick,
        enabled = enabled,
        modifier = Modifier.heightIn(min = MinTarget).semantics { contentDescription = description },
    ) { Text(text) }
}

@Composable
fun Card(modifier: Modifier = Modifier, content: @Composable ColumnScope.() -> Unit) {
    Column(
        modifier = modifier.fillMaxWidth()
            .background(ClambhookPalette.surface, RoundedCornerShape(14.dp))
            .border(1.dp, ClambhookPalette.border, RoundedCornerShape(14.dp))
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
        content = content,
    )
}

@Composable
fun SectionTitle(text: String) {
    Text(text, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
}

@Composable
fun SecondaryText(text: String, modifier: Modifier = Modifier) {
    Text(text, modifier = modifier, color = ClambhookPalette.muted, style = MaterialTheme.typography.bodyMedium)
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun ActionRow(content: @Composable FlowRowScope.() -> Unit) {
    FlowRow(
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
        content = content,
    )
}

/** Vertically scrolling page body with the standard padding and spacing. */
@Composable
fun ScrollPage(content: @Composable ColumnScope.() -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(18.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
        content = content,
    )
}

/** Fixed (non-scrolling) page body whose last child usually takes the remaining height. */
@Composable
fun FillPage(content: @Composable ColumnScope.() -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize().padding(18.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
        content = content,
    )
}

@Composable
fun LabeledField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    secret: Boolean = false,
    singleLine: Boolean = true,
    minLines: Int = 1,
) {
    OutlinedTextField(
        value = value,
        onValueChange = onValueChange,
        label = { Text(label) },
        singleLine = singleLine,
        minLines = minLines,
        visualTransformation = if (secret) PasswordVisualTransformation() else
            androidx.compose.ui.text.input.VisualTransformation.None,
        keyboardOptions = if (secret) KeyboardOptions(keyboardType = KeyboardType.Password) else KeyboardOptions.Default,
        modifier = modifier.heightIn(min = MinTarget).semantics { contentDescription = label },
    )
}

/** Monospace multi-line document editor (JSON, TOML, PEM). */
@Composable
fun DocumentArea(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    readOnly: Boolean = false,
    minLines: Int = 8,
) {
    OutlinedTextField(
        value = value,
        onValueChange = onValueChange,
        readOnly = readOnly,
        label = { Text(label) },
        minLines = minLines,
        textStyle = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace, fontSize = 13.sp),
        modifier = modifier.fillMaxWidth().semantics { contentDescription = label },
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun Picker(
    label: String,
    options: List<String>,
    selected: String?,
    onSelected: (String) -> Unit,
    modifier: Modifier = Modifier,
    display: (String) -> String = { it },
) {
    var expanded by remember { mutableStateOf(false) }
    ExposedDropdownMenuBox(expanded = expanded, onExpandedChange = { expanded = it }, modifier = modifier) {
        OutlinedTextField(
            value = selected?.let(display).orEmpty(),
            onValueChange = {},
            readOnly = true,
            singleLine = true,
            label = { Text(label) },
            trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded) },
            modifier = Modifier.menuAnchor(MenuAnchorType.PrimaryNotEditable)
                .heightIn(min = MinTarget)
                .semantics { contentDescription = label },
        )
        ExposedDropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            options.forEach { option ->
                DropdownMenuItem(
                    text = { Text(display(option)) },
                    onClick = {
                        expanded = false
                        onSelected(option)
                    },
                    modifier = Modifier.heightIn(min = MinTarget),
                )
            }
        }
    }
}

/** Tabbed sub-pages; each tab body fills the remaining space. */
@Composable
fun Tabs(vararg tabs: Pair<String, @Composable () -> Unit>) {
    var selected by rememberSaveable { mutableIntStateOf(0) }
    Column(Modifier.fillMaxSize()) {
        PrimaryScrollableTabRow(selectedTabIndex = selected, edgePadding = 12.dp) {
            tabs.forEachIndexed { index, (title, _) ->
                Tab(
                    selected = index == selected,
                    onClick = { selected = index },
                    text = { Text(title) },
                    modifier = Modifier.heightIn(min = MinTarget),
                )
            }
        }
        Box(Modifier.weight(1f).fillMaxWidth()) { tabs[selected].second() }
    }
}

data class TableColumn<T>(val title: String, val weight: Float, val value: (T) -> String)

/** Accessible, selectable data table backed by a lazy list. */
@Composable
fun <T> DataTable(
    description: String,
    columns: List<TableColumn<T>>,
    rows: List<T>,
    modifier: Modifier = Modifier,
    key: ((T) -> Any)? = null,
    selected: T? = null,
    onSelect: ((T) -> Unit)? = null,
) {
    Column(
        modifier.fillMaxWidth()
            .border(1.dp, ClambhookPalette.border, RoundedCornerShape(10.dp))
            .semantics { contentDescription = description },
    ) {
        Row(Modifier.fillMaxWidth().background(ClambhookPalette.surfaceRaised).padding(horizontal = 12.dp, vertical = 10.dp)) {
            columns.forEach { column ->
                Text(
                    column.title,
                    modifier = Modifier.weight(column.weight),
                    fontWeight = FontWeight.Bold,
                    color = ClambhookPalette.muted,
                    maxLines = 1,
                )
            }
        }
        if (rows.isEmpty()) {
            Box(Modifier.fillMaxWidth().padding(16.dp)) { SecondaryText("No data") }
            return@Column
        }
        LazyColumn(Modifier.fillMaxWidth().weight(1f, fill = false)) {
            items(rows, key = key) { row ->
                val isSelected = row == selected
                val rowModifier = if (onSelect != null) {
                    Modifier.selectable(selected = isSelected, role = Role.Button) { onSelect(row) }
                } else {
                    Modifier
                }
                Row(
                    rowModifier.fillMaxWidth().heightIn(min = MinTarget)
                        .background(if (isSelected) ClambhookPalette.accentDark else ClambhookPalette.background)
                        .padding(horizontal = 12.dp, vertical = 8.dp),
                ) {
                    columns.forEach { column ->
                        Text(
                            column.value(row),
                            modifier = Modifier.weight(column.weight),
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            }
        }
    }
}

@Composable
fun DetailRow(label: String, value: String) {
    Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(label, modifier = Modifier.width(160.dp), color = ClambhookPalette.muted, fontWeight = FontWeight.Bold)
        Text(value)
    }
}

@Composable
fun SelectableList(
    description: String,
    items: List<String>,
    selected: String?,
    onSelect: (String) -> Unit,
) {
    Column(
        Modifier.fillMaxWidth()
            .border(1.dp, ClambhookPalette.border, RoundedCornerShape(10.dp))
            .semantics { contentDescription = description },
    ) {
        if (items.isEmpty()) {
            Box(Modifier.padding(16.dp)) { SecondaryText("No profiles") }
        }
        items.forEach { item ->
            Text(
                item,
                modifier = Modifier.fillMaxWidth().heightIn(min = MinTarget)
                    .selectable(selected = item == selected, role = Role.RadioButton) { onSelect(item) }
                    .background(if (item == selected) ClambhookPalette.accentDark else ClambhookPalette.background)
                    .padding(horizontal = 14.dp, vertical = 14.dp),
            )
        }
    }
}
