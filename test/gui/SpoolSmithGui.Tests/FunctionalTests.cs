using System;
using System.IO;
using System.IO.Compression;
using System.Linq;
using System.Threading;
using FlaUI.Core.AutomationElements;
using FlaUI.Core.Definitions;
using Xunit;

namespace SpoolSmithGui.Tests;

/// <summary>
/// End-to-end checks that a click in the GUI actually reaches the same
/// internal/* packages the CLI's own tests exercise, using only fast,
/// deterministic fixture and loopback paths. Navigation, profile selection,
/// and previews never confirm an install or mutate Windows printers.
/// </summary>
public sealed class FunctionalTests : IDisposable
{
    private readonly AppFixture _fixture;
    private readonly string _testDirectory;

    public FunctionalTests()
    {
        _testDirectory = Path.Combine(Path.GetTempPath(), "spoolsmith-gui-test-" + Guid.NewGuid().ToString("N"));
        _fixture = new AppFixture(profilesDirectory: _testDirectory);
    }

    public void Dispose()
    {
        _fixture.Dispose();
        if (Directory.Exists(_testDirectory)) Directory.Delete(_testDirectory, recursive: true);
    }

    [StaFact]
    public void MainWindow_launches_with_expected_title()
    {
        Assert.Equal("SpoolSmith", _fixture.MainWindow.Title);
        Assert.False(Find(_fixture.MainWindow, "thispc-list").IsOffscreen);
        var visibleTabs = _fixture.MainWindow.FindAllDescendants(cf => cf.ByControlType(ControlType.TabItem))
            .Where(tab => !tab.IsOffscreen).Select(tab => tab.Name).ToArray();
        Assert.Equal(new[] { "This PC", "Add a printer", "Review and apply", "Tools" }, visibleTabs);
    }

    [StaFact]
    public void Bulk_copy_shows_inventory_and_can_be_cancelled_without_exporting()
    {
        var list = Find(_fixture.MainWindow, "thispc-list").AsListBox();
        var copyAll = FindButton(_fixture.MainWindow, "Copy all printers...");
        WaitUntil(() => FindButton(_fixture.MainWindow, "Refresh").IsEnabled,
            "Printer inventory did not finish loading.");
        if (list.Items.Length == 0)
        {
            Assert.False(copyAll.IsEnabled);
            return;
        }

        copyAll.Invoke();
        Window? dialog = null;
        WaitUntil(() => (dialog = _fixture.MainWindow.ModalWindows.Concat(_fixture.App.GetAllTopLevelWindows(_fixture.Automation))
            .FirstOrDefault(w => w.Title == "Copy all printers")) != null, "Bulk copy did not open.");
        var folder = Path.Combine(_testDirectory, "cancelled-export");
        SetText(Find(dialog!, "copy-all-folder").AsTextBox(), folder);
        Assert.Contains("will be skipped", Find(dialog!, "copy-all-details").AsTextBox().Text);
        // Native checkboxes report their visible caption as their UIA Name
        // regardless of an Accessibility.Name override (confirmed against a
        // real Windows run: the "copy-all-drivers" override never reaches
        // UIA for this control type), so this is the only reliable lookup --
        // by control type, since the dialog has exactly one checkbox.
        var driverCheckbox = dialog!.FindFirstDescendant(cf => cf.ByControlType(ControlType.CheckBox));
        Assert.NotNull(driverCheckbox);
        Assert.False(driverCheckbox!.IsOffscreen);
        Assert.False(FindButton(dialog!, "Copy results / JSON").IsEnabled);
        Assert.True(FindButton(dialog!, "Copy printers").IsEnabled);
        FindButton(dialog!, "Cancel").Invoke();
        WaitUntil(() => _fixture.MainWindow.IsEnabled, "Bulk copy did not close.");
        Assert.False(Directory.Exists(folder));
    }

    [StaFact]
    public void Scan_completes_and_populates_discovery_results()
    {
        _fixture.SelectTab("Add a printer");
        // CI only probes loopback. A local operator can opt into a real subnet
        // and require a known candidate to appear in the native results list.
        var cidr = Environment.GetEnvironmentVariable("SPOOLSMITH_DISCOVERY_CIDR") ?? "127.0.0.1/32";
        var expectedIP = Environment.GetEnvironmentVariable("SPOOLSMITH_DISCOVERY_EXPECT_IP");
        SetText(Find(_fixture.MainWindow, "discover-cidr").AsTextBox(), cidr);
        var scan = FindButton(_fixture.MainWindow, "Scan");
        scan.Invoke();
        var output = Find(_fixture.MainWindow, "discover-output").AsTextBox();
        // The results pane states the outcome in plain language; the raw
        // scan JSON moved behind "Scan details", which must become available.
        var text = WaitForText(output, t => t.Contains("after checking"), 150_000);
        Assert.Contains(cidr, text);
        Assert.True(scan.IsEnabled);
        Assert.False(FindButton(_fixture.MainWindow, "Cancel scan").IsEnabled);
        Assert.True(FindButton(_fixture.MainWindow, "Scan details").IsEnabled);
        if (!string.IsNullOrWhiteSpace(expectedIP))
        {
            var list = Find(_fixture.MainWindow, "discover-results").AsListBox();
            Assert.Contains(list.Items, item => item.Text.Contains(expectedIP));
        }
    }

    [StaFact]
    public void Catalog_list_families_populates_output()
    {
        _fixture.SelectTab("Catalog");

        var button = FindButton(_fixture.MainWindow, "List families");
        var output = Find(_fixture.MainWindow, "catalog-output").AsTextBox();

        button.Invoke();

        var text = WaitForText(output, t => t.Contains("hp-laserjet", StringComparison.OrdinalIgnoreCase));
        Assert.Contains("brother-hl-l2xxx", text, StringComparison.OrdinalIgnoreCase);
    }

    [StaFact]
    public void Inspect_fixture_file_resolves_a_family()
    {
        _fixture.SelectTab("Inspect");

        var target = Find(_fixture.MainWindow, "inspect-target").AsTextBox();
        var button = FindButton(_fixture.MainWindow, "Inspect");
        var output = Find(_fixture.MainWindow, "inspect-output").AsTextBox();

        var fixturePath = _fixture.FixturePath("hp-laserjet-m404-synthetic.json");
        Assert.True(File.Exists(fixturePath), $"expected fixture at {fixturePath}");

        SetText(target, fixturePath);
        Assert.Equal(fixturePath, target.Text);
        button.Invoke();

        string text;
        try { text = WaitForText(output, t => t.Contains("confidence", StringComparison.OrdinalIgnoreCase)); }
        catch (TimeoutException ex)
        {
            _fixture.MainWindow.CaptureToFile(Path.Combine(_fixture.RepoRoot, "dist", "inspect-failure.png"));
            var controls = _fixture.MainWindow.FindAllDescendants();
            throw new TimeoutException(ex.Message + "\nButton: " + button.BoundingRectangle + " enabled=" + button.IsEnabled + "\nControls: " + string.Join(" | ", System.Linq.Enumerable.Select(controls, c => c.ControlType + ":" + c.Name)), ex);
        }
        Assert.Contains("hp-laserjet-m4xx", text, StringComparison.OrdinalIgnoreCase);
    }

    [StaFact]
    public void Empty_review_cannot_enable_execution()
    {
        _fixture.SelectTab("Review and apply");
        Assert.False(FindButton(_fixture.MainWindow, "Apply").IsEnabled);
        Assert.False(FindButton(_fixture.MainWindow, "Preview changes").IsEnabled);
    }

    [StaFact]
    public void Direct_IP_opens_settings_on_the_same_page()
    {
        _fixture.SelectTab("Add a printer");
        SetText(Find(_fixture.MainWindow, "discover-cidr").AsTextBox(), "192.0.2.40");
        FindButton(_fixture.MainWindow, "Use IP directly").Invoke();
        WaitUntil(() => !IsHidden("capture-target"), "Settings did not open.");
        Assert.Equal("192.0.2.40", Find(_fixture.MainWindow, "capture-target").AsTextBox().Text);
        _fixture.MainWindow.CaptureToFile(Path.Combine(_fixture.RepoRoot,"dist","gui-direct-ip.png"));
        Assert.False(FindButton(_fixture.MainWindow, "Save and review").IsOffscreen);
    }

    [StaFact]
    public void Optional_panels_stay_hidden_until_requested()
    {
        _fixture.SelectTab("Add a printer");
        Assert.True(IsHidden("capture-target"));
        _fixture.SelectTab("Review and apply");
        Assert.True(IsHidden("Preview only (never apply)"));
        Find(_fixture.MainWindow, "More options").AsCheckBox().Click();
        WaitUntil(() => !IsHidden("Preview only (never apply)"), "More options did not open.");
        Assert.True(IsHidden("Also remove the driver, if nothing else uses it"));
        Assert.True(IsHidden("Offline setup — the printer will not be contacted or checked"));
    }

    private bool IsHidden(string accessibleName)
    {
        var element = _fixture.MainWindow.FindFirstDescendant(cf => cf.ByName(accessibleName));
        return element == null || element.IsOffscreen;
    }

    private Window OpenSavedSetups()
    {
        _fixture.SelectTab("Add a printer");
        FindButton(_fixture.MainWindow, "Open a saved setup...").Invoke();
        Window? dialog = null;
        try { WaitUntil(() => (dialog = _fixture.MainWindow.ModalWindows.Concat(_fixture.App.GetAllTopLevelWindows(_fixture.Automation))
            .FirstOrDefault(w => w.Title == "Saved printer setups")) != null, "Saved setups did not open."); }
        catch { _fixture.MainWindow.CaptureToFile(Path.Combine(_fixture.RepoRoot,"dist","saved-dialog-failure.png")); throw; }
        return dialog!;
    }

    [StaFact]
    public void Empty_saved_folder_exposes_import_without_creating_files()
    {
        var dialog = OpenSavedSetups();
        Assert.Contains("No saved setups", Find(dialog, "saved-detail").AsTextBox().Text);
        Assert.False(FindButton(dialog, "Set up this printer").IsEnabled);
        Assert.True(FindButton(dialog, "Import all...").IsEnabled);
        Assert.True(FindButton(dialog, "Export all...").IsEnabled);
        FindButton(dialog, "Close").Invoke();
        WaitUntil(() => !_fixture.MainWindow.ModalWindows.Any(), "Close did not dismiss saved setups.");
        Assert.False(Directory.Exists(_testDirectory));
    }

    [StaFact]
    public void Invalid_saved_setup_cannot_be_edited_or_applied()
    {
        var path = CreateSavedPrinter();
        var invalid = ReadManifestEntry(path).Replace("\"version\": 1", "\"unknown_future_field\": true, \"version\": 1");
        WriteManifestEntry(path, invalid);
        var dialog = OpenSavedSetups();
        Assert.Contains("cannot be used", Find(dialog, "saved-detail").AsTextBox().Text);
        foreach (var caption in new[] { "Set up this printer", "Update to match", "Check status", "Remove printer from this PC...", "Edit..." })
            Assert.False(FindButton(dialog, caption).IsEnabled);
        FindButton(dialog, "Close").Invoke();
        // Merely listing/viewing the corrupted file must not have "fixed" it.
        Assert.Equal(invalid, ReadManifestEntry(path));
    }

    private static string ReadManifestEntry(string bundlePath)
    {
        using var archive = ZipFile.OpenRead(bundlePath);
        using var reader = new StreamReader(archive.GetEntry("manifest.json")!.Open());
        return reader.ReadToEnd();
    }

    private static void WriteManifestEntry(string bundlePath, string manifestJson)
    {
        using var archive = ZipFile.Open(bundlePath, ZipArchiveMode.Update);
        archive.GetEntry("manifest.json")!.Delete();
        using var writer = new StreamWriter(archive.CreateEntry("manifest.json").Open());
        writer.Write(manifestJson);
    }

    [StaTheory]
    [InlineData("Set up this printer", "Add printer")]
    [InlineData("Update to match", "Update printer")]
    [InlineData("Remove printer from this PC...", "Remove printer")]
    public void Saved_setup_hands_the_named_operation_to_review(string action, string applyCaption)
    {
        CreateSavedPrinter();
        var dialog = OpenSavedSetups();
        Assert.Contains("Test office printer", Find(dialog, "saved-detail").AsTextBox().Text);
        FindButton(dialog, action).Invoke();
        try { WaitUntil(() => !IsHidden("mutate-output"), "Review did not open."); }
        catch { _fixture.MainWindow.CaptureToFile(Path.Combine(_fixture.RepoRoot,"dist","review-handoff-failure.png")); throw; }
        Assert.Contains(_fixture.MainWindow.FindAllDescendants(cf => cf.ByControlType(ControlType.Text)),
            label => label.Name.Contains("Test office printer", StringComparison.Ordinal) && !label.IsOffscreen);
        Assert.False(FindButton(_fixture.MainWindow, applyCaption).IsEnabled);
        Assert.True(FindButton(_fixture.MainWindow, "Preview changes").IsEnabled);
    }

    [StaFact]
    public void Offline_profile_preview_reports_missing_driver_and_keeps_apply_disabled()
    {
        CreateSavedPrinter();
        var dialog = OpenSavedSetups();
        FindButton(dialog, "Set up this printer").Invoke();
        WaitUntil(() => !IsHidden("mutate-output"), "Review did not open.");
        Find(_fixture.MainWindow, "More options").AsCheckBox().Click();
        Find(_fixture.MainWindow, "Offline setup — the printer will not be contacted or checked").AsCheckBox().Click();
        FindButton(_fixture.MainWindow, "Preview changes").Invoke();
        var output = Find(_fixture.MainWindow, "mutate-output").AsTextBox();
        WaitForText(output, t => t.Contains("Unable to continue"), 60_000);
        Assert.Contains("driver", output.Text, StringComparison.OrdinalIgnoreCase);
        Assert.False(FindButton(_fixture.MainWindow, "Add printer").IsEnabled);
        Assert.True(FindButton(_fixture.MainWindow, "Full plan / JSON").IsEnabled);
    }

    private Window OpenIntuneWizard()
    {
        _fixture.SelectTab("Tools");
        FindButton(_fixture.MainWindow, "Build an Intune printer app...").Invoke();
        Window? dialog = null;
        try
        {
            WaitUntil(() => (dialog = _fixture.MainWindow.ModalWindows.Concat(_fixture.App.GetAllTopLevelWindows(_fixture.Automation))
                .FirstOrDefault(w => w.Title == "Build an Intune printer app")) != null, "Intune wizard did not open.");
            // The dialog's owned Window can resolve before its first page's
            // controls are queryable via UIA; wait for one before handing back.
            FindVisibleIntuneControl(dialog!, "intune-profile");
        }
        catch { _fixture.MainWindow.CaptureToFile(Path.Combine(_fixture.RepoRoot,"dist","intune-wizard-failure.png")); throw; }
        return dialog!;
    }

    [StaFact]
    public void Intune_wizard_button_is_enabled_and_opens_dialog()
    {
        _fixture.SelectTab("Tools");
        Assert.True(FindButton(_fixture.MainWindow, "Build an Intune printer app...").IsEnabled);

        var dialog = OpenIntuneWizard();
        FindVisibleIntuneControl(dialog, "intune-profile");
        var advanced = FindVisibleIntuneControl(dialog, "Advanced settings (updates, metadata and policy)").AsCheckBox();
        Assert.Equal<bool?>(false, advanced.IsChecked);
        AssertIntuneAdvancedHidden(dialog);

        // Absence while collapsed is valid UIA behavior, but absence after
        // expanding is a bug. Exercise both directions, including retained data.
        dialog.SetForeground();
        advanced.Click();
        foreach (var field in IntuneAdvancedFields)
            Assert.True(FindVisibleIntuneControl(dialog, field).IsEnabled);
        SetText(FindVisibleIntuneControl(dialog, "intune-id").AsTextBox(), "retained-id");
        advanced.Click();
        AssertIntuneAdvancedHidden(dialog);
        advanced.Click();
        Assert.Equal("retained-id", FindVisibleIntuneControl(dialog, "intune-id").AsTextBox().Text);
        FindButton(dialog, "Close").Invoke();
        WaitUntil(() => !_fixture.MainWindow.ModalWindows.Any(), "Close did not dismiss the Intune wizard.");
    }

    /// <summary>
    /// Drives the wizard's full happy path end to end: local profile and CLI
    /// binary in, reviewed package out. Prepare/Export never contact a printer
    /// or a tenant (see internal/intune's package doc comment), so this stays
    /// within the suite's no-real-mutation constraint despite writing files.
    /// </summary>
    [StaFact]
    public void Intune_wizard_exports_a_local_only_package()
    {
        Assert.True(File.Exists(_fixture.CliExePath),
            $"expected the Windows CLI binary at {_fixture.CliExePath} (build with: go build -o dist/spoolsmith.exe ./cmd/spoolsmith, or set SPOOLSMITH_CLI_EXE)");
        var profilePath = Path.Combine(_fixture.RepoRoot, "examples", "intune", "accounting.ssb");
        Assert.True(File.Exists(profilePath), $"expected example profile at {profilePath}");
        var outputDir = Path.Combine(Path.GetTempPath(), "spoolsmith-intune-test-" + Guid.NewGuid().ToString("N"));

        var dialog = OpenIntuneWizard();
        try
        {
            SetText(Find(dialog, "intune-profile").AsTextBox(), profilePath);
            SetText(Find(dialog, "intune-binary").AsTextBox(), _fixture.CliExePath);
            // The bundled example profile carries no local driver archive.
            Find(dialog, "Driver is managed separately and will be registered before installation").AsCheckBox().Click();
            WaitForText(Find(dialog, "intune-display-name").AsTextBox(),
                t => t == "Example — Accounting Copier");
            Assert.False(string.IsNullOrWhiteSpace(Find(dialog, "intune-output").AsTextBox().Text));
            // The common path needs no metadata, hash calculation or extra page.
            SetText(Find(dialog, "intune-output").AsTextBox(), outputDir);
            FindButton(dialog, "Validate and preview package").Invoke();

            var preview = FindVisibleIntuneControl(dialog, "intune-preview").AsTextBox();
            WaitForText(preview, t => t.Contains("example-accounting-copier-", StringComparison.Ordinal));
            var previewText = preview.Text!;
            using (var manifest = System.Text.Json.JsonDocument.Parse(previewText[previewText.IndexOf('{')..]))
            {
                Assert.Equal(1, manifest.RootElement.GetProperty("revision").GetInt32());
                Assert.False(manifest.RootElement.GetProperty("offline").GetBoolean());
                Assert.False(manifest.RootElement.GetProperty("adopt_matching_queue").GetBoolean());
                var expectedHash = Convert.ToHexString(System.Security.Cryptography.SHA256.HashData(File.ReadAllBytes(_fixture.CliExePath))).ToLowerInvariant();
                Assert.Equal(expectedHash, manifest.RootElement.GetProperty("binary_sha256").GetString());
            }
            Assert.False(Directory.Exists(outputDir), "preview wrote files before confirmation");
            // Returning to settings must invalidate approval, even without edits.
            FindButton(dialog, "Back to settings").Invoke();
            FindVisibleIntuneControl(dialog, "intune-profile");
            // Do not query a button under a programmatically hidden TabPage.
            // Reopen review WITHOUT validating: this must not restore approval.
            SelectIntuneReviewPage(dialog);
            Assert.False(FindButton(dialog, "Export reviewed package").IsEnabled);
            Assert.False(Directory.Exists(outputDir), "returning to review exported an unconfirmed package");
            FindButton(dialog, "Back to settings").Invoke();
            FindVisibleIntuneControl(dialog, "Advanced settings (updates, metadata and policy)").AsCheckBox().Click();
            SetText(FindVisibleIntuneControl(dialog, "intune-id").AsTextBox(), "gui-wizard-test");
            SetText(FindVisibleIntuneControl(dialog, "intune-revision").AsTextBox(), "2");
            SetText(FindVisibleIntuneControl(dialog, "intune-location").AsTextBox(), "West");
            SetText(FindVisibleIntuneControl(dialog, "intune-description").AsTextBox(), "Updated metadata");
            SetText(Find(dialog, "intune-display-name").AsTextBox(), "GUI wizard test");
            SelectIntuneReviewPage(dialog);
            Assert.False(FindButton(dialog, "Export reviewed package").IsEnabled);
            FindButton(dialog, "Back to settings").Invoke();
            FindButton(dialog, "Validate and preview package").Invoke();
            // Reacquire after navigation; an old UIA wrapper need not survive hiding.
            preview = FindVisibleIntuneControl(dialog, "intune-preview").AsTextBox();
            WaitForText(preview, t => t.Contains("GUI wizard test", StringComparison.Ordinal));
            Assert.True(FindButton(dialog, "Export reviewed package").IsEnabled);
            FindButton(dialog, "Export reviewed package").Invoke();

            Window? confirm = null;
            try { WaitUntil(() => (confirm = _fixture.MainWindow.ModalWindows.Concat(_fixture.App.GetAllTopLevelWindows(_fixture.Automation))
                .FirstOrDefault(w => w.Title == "Package exported")) != null, "Export confirmation did not appear."); }
            catch { _fixture.MainWindow.CaptureToFile(Path.Combine(_fixture.RepoRoot,"dist","intune-export-failure.png")); throw; }
            FindButton(confirm!, "OK").Invoke();
            WaitUntil(() => !(_fixture.MainWindow.ModalWindows.Concat(_fixture.App.GetAllTopLevelWindows(_fixture.Automation)).Any(w => w.Title == "Package exported")),
                "Export confirmation did not close.");

            FindButton(dialog, "Close").Invoke();
            WaitUntil(() => !_fixture.MainWindow.ModalWindows.Any(), "Close did not dismiss the Intune wizard.");

            foreach (var expected in new[] { "install.ps1", "uninstall.ps1", "detect.ps1", "runtime.ps1", "README.txt", "profile.json", "deployment.json", "spoolsmith.exe" })
            {
                Assert.True(File.Exists(Path.Combine(outputDir, expected)), $"expected exported {expected}");
            }
            Assert.False(File.Exists(Path.Combine(outputDir, "driver.exe")), "unexpected driver.exe: the prerequisite path bundles no driver");
            using var exported = System.Text.Json.JsonDocument.Parse(File.ReadAllText(Path.Combine(outputDir, "deployment.json")));
            Assert.Equal("gui-wizard-test", exported.RootElement.GetProperty("id").GetString());
            Assert.Equal(2, exported.RootElement.GetProperty("revision").GetInt32());
            Assert.Equal("West", exported.RootElement.GetProperty("location").GetString());
            Assert.Equal("Updated metadata", exported.RootElement.GetProperty("description").GetString());
        }
        finally
        {
            if (Directory.Exists(outputDir)) Directory.Delete(outputDir, recursive: true);
        }
    }

    /// <summary>
    /// The review page can also run Microsoft's Win32 Content Prep Tool to make the
    /// .intunewin. That needs the real IntuneWinAppUtil.exe, which the repo does not
    /// carry, so this runs only when SPOOLSMITH_CONTENT_PREP_EXE points at it.
    /// </summary>
    [StaFact]
    public void Intune_wizard_creates_the_intunewin_with_the_content_prep_tool()
    {
        var tool = Environment.GetEnvironmentVariable("SPOOLSMITH_CONTENT_PREP_EXE");
        if (string.IsNullOrEmpty(tool)) return;
        Assert.True(File.Exists(tool), $"SPOOLSMITH_CONTENT_PREP_EXE does not exist: {tool}");
        var profilePath = Path.Combine(_fixture.RepoRoot, "examples", "intune", "accounting.ssb");
        var outputDir = Path.Combine(Path.GetTempPath(), "spoolsmith-intune-prep-" + Guid.NewGuid().ToString("N"));
        var prepDir = outputDir + "-intunewin";

        var dialog = OpenIntuneWizard();
        try
        {
            SetText(Find(dialog, "intune-profile").AsTextBox(), profilePath);
            SetText(Find(dialog, "intune-binary").AsTextBox(), _fixture.CliExePath);
            Find(dialog, "Driver is managed separately and will be registered before installation").AsCheckBox().Click();
            WaitForText(Find(dialog, "intune-display-name").AsTextBox(), t => t == "Example — Accounting Copier");
            SetText(Find(dialog, "intune-output").AsTextBox(), outputDir);
            FindButton(dialog, "Validate and preview package").Invoke();
            WaitForText(FindVisibleIntuneControl(dialog, "intune-preview").AsTextBox(),
                t => t.Contains("example-accounting-copier-", StringComparison.Ordinal));

            // The package folder is suggested beside the export folder, and nothing runs yet.
            var prepOutput = FindVisibleIntuneControl(dialog, "intune-prep-output").AsTextBox();
            WaitForText(prepOutput, t => t == prepDir);
            SetText(FindVisibleIntuneControl(dialog, "intune-prep-tool").AsTextBox(), tool);
            Assert.False(FindButton(dialog, "Create .intunewin from the exported folder").IsEnabled);

            FindButton(dialog, "Export reviewed package").Invoke();
            Window? ready = null;
            WaitUntil(() => (ready = _fixture.MainWindow.ModalWindows.Concat(_fixture.App.GetAllTopLevelWindows(_fixture.Automation))
                .FirstOrDefault(w => w.Title == "Package ready" || w.Title == "Content prep failed")) != null,
                "Content prep did not finish.", timeoutMs: 120_000);
            Assert.Equal("Package ready", ready!.Title);
            FindButton(ready, "OK").Invoke();

            Assert.True(File.Exists(Path.Combine(outputDir, "deployment.json")), "the export folder was not written");
            Assert.True(File.Exists(Path.Combine(prepDir, "install.intunewin")), "the .intunewin was not created");
            // Once exported, the package can be re-created without exporting again.
            WaitUntil(() => FindButton(dialog, "Create .intunewin from the exported folder").IsEnabled,
                "the re-create button never became available after export");
            Assert.False(FindButton(dialog, "Export reviewed package").IsEnabled);

            FindButton(dialog, "Close").Invoke();
            WaitUntil(() => !_fixture.MainWindow.ModalWindows.Any(), "Close did not dismiss the Intune wizard.");
        }
        finally
        {
            if (Directory.Exists(outputDir)) Directory.Delete(outputDir, recursive: true);
            if (Directory.Exists(prepDir)) Directory.Delete(prepDir, recursive: true);
        }
    }

    private static readonly string[] IntuneAdvancedFields =
    {
        "intune-id", "intune-revision", "intune-location", "intune-description", "intune-binary-sha256",
        "Offline setup — the installed app won't check the printer is really there",
        "Allow adoption of an existing, exactly matching unmanaged queue"
    };

    private static void AssertIntuneAdvancedHidden(Window dialog)
    {
        // Walk eagerly creates native HWNDs, but UIA2 may omit their subtree
        // after ShowWindow(SW_HIDE). Missing and offscreen both mean hidden.
        // The opening test separately requires every field to appear on expand.
        // https://learn.microsoft.com/en-us/dotnet/api/system.windows.automation.automationelement.automationelementinformation.isoffscreen
        WaitUntil(() => IntuneAdvancedFields.All(name =>
        {
            var element = dialog.FindFirstDescendant(cf => cf.ByName(name));
            return element == null || element.IsOffscreen;
        }), "Advanced settings were not collapsed.");
    }

    internal static AutomationElement FindVisibleIntuneControl(Window dialog, string name)
    {
        AutomationElement? element = null;
        WaitUntil(() =>
        {
            element = dialog.FindFirstDescendant(cf => cf.ByName(name));
            return element != null && !element.IsOffscreen;
        }, $"Intune control '{name}' did not become visible.");
        return element!;
    }

    private static void SelectIntuneReviewPage(Window dialog)
    {
        // As in AppFixture.SelectTab, use a genuine click: SelectionItemPattern
        // does not send the TCN_SELCHANGE that Walk needs to swap visible pages.
        var tab = dialog.FindFirstDescendant(cf => cf.ByControlType(ControlType.TabItem)
            .And(cf.ByName("2. Review and export")))
            ?? throw new InvalidOperationException("Intune review tab was not found.");
        dialog.SetForeground();
        dialog.Focus();
        tab.Click();
        FindVisibleIntuneControl(dialog, "intune-preview");
    }

    private string CreateSavedPrinter()
    {
        Directory.CreateDirectory(_testDirectory);
        var path = Path.Combine(_testDirectory, "office-printer.ssb");
        WriteProfileBundle(path, """
            {
              "version": 1,
              "target": "127.0.0.1",
              "printer_name": "Test office printer",
              "driver_name": "Test driver",
              "evidence": {
                "ip": "127.0.0.1",
                "http_title": "Test printer identity",
                "provenance": "captured"
              }
            }
            """);
        return path;
    }

    /// <summary>
    /// Builds a minimal, valid printer file directly -- a zip carrying
    /// manifest.json with no driver payload, the same shape
    /// internal/bundle.SaveProfile produces -- without a live network probe
    /// or running the Go CLI as a fixture-building step.
    /// </summary>
    private static void WriteProfileBundle(string path, string profileJson)
    {
        var manifest = $$"""
            {
              "version": 1,
              "created_utc": "2026-01-01T00:00:00Z",
              "profile": {{profileJson.Trim()}}
            }
            """;
        using var archive = ZipFile.Open(path, ZipArchiveMode.Create);
        using var writer = new StreamWriter(archive.CreateEntry("manifest.json").Open());
        writer.Write(manifest);
    }

    internal static void WaitUntil(Func<bool> ready, string failure, int timeoutMs = 10_000)
    {
        var deadline = DateTime.UtcNow.AddMilliseconds(timeoutMs);
        while (DateTime.UtcNow < deadline)
        {
            if (ready()) return;
            Thread.Sleep(100);
        }
        throw new TimeoutException(failure);
    }

    internal static AutomationElement Find(AutomationElement root, string accessibleName)
    {
        return root.FindFirstDescendant(cf => cf.ByName(accessibleName))
            ?? throw new InvalidOperationException($"No control with accessible name \"{accessibleName}\" was found.");
    }

    /// <summary>
    /// A plain ByName search for a caption like "Inspect" is ambiguous — the
    /// Inspect tab item shares that exact Name with the Inspect button inside
    /// it — so button lookups constrain to ControlType.Button explicitly.
    ///
    /// Polls rather than asserting on the first frame. Walk lays a tab page out
    /// after the native TCN_SELCHANGE it reacts to, and the apply button's
    /// caption is rewritten at runtime by updateReviewControls, so immediately
    /// after a navigation a button can still report IsOffscreen or carry its
    /// previous name. Observed directly: these lookups passed in isolation and
    /// failed only in a full run, where the preceding test's window teardown
    /// shifts the timing.
    /// </summary>
    internal static Button FindButton(AutomationElement root, string caption, int timeoutMs = 5_000)
    {
        var deadline = DateTime.UtcNow.AddMilliseconds(timeoutMs);
        while (true)
        {
            var element = root.FindAllDescendants(cf => cf.ByControlType(ControlType.Button).And(cf.ByName(caption)))
                .FirstOrDefault(button => !button.IsOffscreen);
            if (element != null) return element.AsButton();
            if (DateTime.UtcNow >= deadline)
            {
                throw new InvalidOperationException($"No button captioned \"{caption}\" was found.");
            }
            Thread.Sleep(100);
        }
    }

    /// <summary>
    /// TextBox.Text's setter can fall back to simulated keystrokes, which
    /// (like a synthetic Click()) only land if this window happens to be
    /// foreground — confirmed directly: it silently left the real control
    /// empty, and the app's own "target required" guard fired instead of
    /// running Inspect. ValuePattern.SetValue drives the control directly
    /// through UI Automation regardless of focus or window z-order.
    /// </summary>
    internal static void SetText(TextBox box, string value)
    {
        var pattern = box.Patterns.Value.PatternOrDefault
            ?? throw new InvalidOperationException("Control does not support ValuePattern.");
        pattern.SetValue(value);
    }

    /// <summary>
    /// GUI actions that call into probe/install run on a goroutine and land
    /// via walk.Synchronize, so the TextEdit updates a beat after Invoke()
    /// returns. Poll instead of sleeping a fixed amount.
    /// </summary>
    internal static string WaitForText(TextBox box, Func<string, bool> ready, int timeoutMs = 10_000)
    {
        var deadline = DateTime.UtcNow.AddMilliseconds(timeoutMs);
        string last = box.Text ?? string.Empty;
        while (DateTime.UtcNow < deadline)
        {
            last = box.Text ?? string.Empty;
            if (ready(last))
            {
                return last;
            }
            Thread.Sleep(100);
        }
        throw new TimeoutException($"Timed out waiting for expected output. Last seen text:\n{last}");
    }
}
